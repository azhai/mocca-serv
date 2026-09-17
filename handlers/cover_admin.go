//go:build !noweb

package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// 封面图制作是管理后台的能力，因此和后台一起在编译期剥离：
// 本文件只在带后台的构建里编译，纯 API 构建走同包的 cover_api.go。

// MaxCoverBytes 封面图上限 2MB。后台是在浏览器里现画一张 1280×720 的图，
// 正常只有几百 KB；留这个上限是挡住「拿原图当封面直传」这类用法。
const MaxCoverBytes int64 = 2 << 20

// coverMIMEs 允许落盘的封面类型 → 扩展名。
// 只认这三种：数据目录里不该出现按字节嗅探不出类型的文件。
var coverMIMEs = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

// coverNameChars 主文件名里允许出现的字符：中日韩汉字、拉丁字母、数字与 -_. ，
// 其余一律换成连字符。挡住的不只是 ../，还有 "" 0x00、控制字符这类
// 在某些文件系统上会被截断的名字。
var coverNameChars = regexp.MustCompile(`[^\p{Han}\p{Hiragana}\p{Katakana}\p{Latin}\p{Nd}._-]`)

// SaveCover 保存后台画好的封面图，返回落库用的相对路径（口径同 models.CoverRelPath）。
//
// 为什么不复用 /fs/put：上传只能写进**已挂载的存储**，而封面按约定要落在
// <数据目录>/.mocca/covers/ 下 —— 与媒体库解耦，数据目录搬家、挂载点增删都不影响它。
//
// 只负责落盘、不做转码：图是浏览器 canvas 画好的，服务端没必要引入图像库。
func SaveCover(c *echo.Context) error {
	fh, err := firstCoverFile(c)
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	if fh.Size > MaxCoverBytes {
		return helpers.Fail(c, helpers.CodeBadRequest, "封面图不能超过 2MB")
	}

	// 类型不看文件名也不看客户端声明的 Content-Type，只按内容嗅探：
	// 表单里的 filename 与 MIME 都是调用方说了算，信它就等于让任意字节落进数据目录。
	head := make([]byte, 512)
	src, err := fh.Open()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取封面失败")
	}
	defer func() { _ = src.Close() }()

	n, err := io.ReadFull(src, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return helpers.Fail(c, helpers.CodeInternal, "读取封面失败")
	}
	mime := strings.TrimSpace(strings.Split(http.DetectContentType(head[:n]), ";")[0])
	ext, ok := coverMIMEs[mime]
	if !ok {
		return helpers.Fail(c, helpers.CodeBadRequest, "只接受 PNG / JPEG / WebP 图片")
	}

	base := coverBaseName(c.FormValue("name"))
	if base == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请给出合法的封面文件名")
	}

	dir := filepath.Join(config.Cfg.DataDir, models.HiddenDirName, models.CoversSubDirName)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "创建封面目录失败")
	}
	name := base + ext
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入封面失败")
	}
	// 前 512 字节已经被嗅探读走，这里拼回原始字节流，保证落盘的是完整图片
	_, copyErr := io.Copy(out, io.MultiReader(bytes.NewReader(head[:n]), src))
	if closeErr := out.Close(); copyErr != nil || closeErr != nil {
		return helpers.Fail(c, helpers.CodeInternal, "写入封面失败")
	}
	return helpers.OK(c, map[string]string{"cover": models.CoverRelPath(name)})
}

// firstCoverFile 取表单里的封面文件。字段名固定为 file，兼容批量上传用的 files。
func firstCoverFile(c *echo.Context) (*multipart.FileHeader, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return nil, errors.New("请求不是合法的表单上传")
	}
	for _, key := range []string{"file", "files"} {
		if files := form.File[key]; len(files) > 0 {
			return files[0], nil
		}
	}
	return nil, errors.New("没有封面文件")
}

// coverBaseName 把调用方给的名字收敛成安全的主文件名：
// 取基名 → 去掉扩展名与开头的点 → 非法字符换 - → 最多 64 个字符。
// 调用方通常传媒体文件名（a.mp4），出参就是 a，扩展名由服务端按真实类型补。
func coverBaseName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	// 先剥开头的点再切扩展名：对 ".hidden" 这种名字，path.Ext 会把整个名字
	// 当成扩展名返回，顺序反了就会得到空串（隐藏文件也不该由封面目录来建）。
	name = strings.TrimLeft(name, ".")
	name = strings.TrimSuffix(name, path.Ext(name))
	name = coverNameChars.ReplaceAllString(name, "-")
	if r := []rune(strings.Trim(name, "-.")); len(r) > 64 {
		name = string(r[:64])
	} else {
		name = string(r)
	}
	return strings.Trim(name, "-.")
}
