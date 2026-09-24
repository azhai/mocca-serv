package handlers

import (
	"log"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// settingDef 一个全局开关的定义：键、展示文案、缺省值、写入后的副作用。
//
// 列表、缺省值、写入白名单全部从这张表派生 —— 新增一个开关只改这一处，
// 不会出现「列表里有、白名单里忘了」这类三份键名各写一遍的错位。
//
// Def 是函数而不是 bool：这张表在包 init 时就构造，而 config.Cfg 要到
// config.Load() 之后才有值（测试里由用例设置）。写成字段会在 init 阶段
// 直接 nil 解引用，所以缺省值必须延迟到真正读取时才求值。
type settingDef struct {
	Key   string
	Label string
	Hint  string
	Def   func() bool
	Extra func(on bool) // 运行时副作用，可空
}

// always 常量缺省值，省得为「固定 true/false」各写一个闭包。
func always(v bool) func() bool { return func() bool { return v } }

// globalOptions 后台「全局选项」页的开关清单（顺序即展示顺序）。
//
// fs_watch 放在最前：它是唯一有运行时副作用的开关（启停文件监控），
// 缺省关闭是因为 inotify/kqueue 要为每个子目录单独登记，大目录树开销明显。
var globalOptions = []settingDef{
	{
		Key:   models.SettingFsWatch,
		Label: "文件监控（FS Watch）",
		Hint:  "本地存储有文件增删改时自动增量重扫所在目录。默认关闭；仅对本地(Local)存储有效，SMB 等远程存储收不到系统事件。",
		Def:   always(false),
		Extra: func(on bool) {
			if on {
				_ = mediaindex.Watch.Start(log.Printf)
			} else {
				mediaindex.Watch.Stop()
			}
		},
	},
	{
		Key:   SettingAllowGuest,
		Label: "允许游客浏览",
		Hint:  "未登录也能列目录、取流与看图；关闭后浏览接口一律要求登录。",
		Def:   always(true),
	},
	{
		Key:   SettingAllowRegister,
		Label: "开放注册",
		Hint:  "允许自助注册新账号；关闭后只有管理员能在后台建号。",
		Def:   func() bool { return config.Cfg.AllowRegister },
	},
}

// SettingOption 全局开关的对外结构（含当前值）。
type SettingOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
	Value bool   `json:"value"`
}

// ListSettings 列出全局开关与当前值。缺省值来自 settingDef，
// 库里没写过该键时返回缺省值（不落库）。
func ListSettings(c *echo.Context) error {
	out := make([]SettingOption, 0, len(globalOptions))
	for _, o := range globalOptions {
		out = append(out, SettingOption{
			Key:   o.Key,
			Label: o.Label,
			Hint:  o.Hint,
			Value: models.SettingBool(o.Key, o.Def()),
		})
	}
	return helpers.OK(c, out)
}

// UpdateSetting 写入单个全局开关。
func UpdateSetting(c *echo.Context) error {
	var req struct {
		Key   string `json:"key"`
		Value bool   `json:"value"`
	}
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	for _, o := range globalOptions {
		if o.Key != req.Key {
			continue
		}
		if err := models.SetSettingBool(o.Key, req.Value); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "保存失败")
		}
		if o.Extra != nil {
			o.Extra(req.Value)
		}
		return helpers.OK(c, SettingOption{
			Key: o.Key, Label: o.Label, Hint: o.Hint, Value: req.Value,
		})
	}
	// 白名单外一律拒绝：settings 表是通用键值表，放开就等于让后台写任意键
	return helpers.Fail(c, helpers.CodeBadRequest, "未知的选项："+req.Key)
}
