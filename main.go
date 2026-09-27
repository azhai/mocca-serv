package main

import (
	"bufio"
	"context"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/azhai/mocca/config"
	"github.com/azhai/mocca/drivers"
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/mediaindex"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/azhai/mocca/routes"
	"github.com/azhai/mocca/web"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

func main() {
	// 兜底：main goroutine 里的 panic 也要留痕。写进错误日志后按非零码退出，
	// 是否重启交给守护进程（systemd / gorch 之类）决定。
	// 必须直接 defer RecoverExit：包进匿名函数会让 recover 失效（见其注释）。
	defer helpers.RecoverExit("main", 1)

	cfg, err := config.Load()
	if err != nil {
		helpers.Fatalf("启动", "载入配置失败: %+v", errors.WithStack(err))
	}
	if cfg.EnvLoaded {
		log.Printf("已载入配置文件 %s", cfg.EnvFile)
	} else {
		log.Printf("配置文件 %s 不存在，使用环境变量与内置默认值", cfg.EnvFile)
	}

	// 错误日志（panic 与致命错误的落点）：数据目录已知就得先打开，
	// 后面任何一步崩了才有地方记。打开失败只提醒，不阻断启动。
	if err := helpers.OpenErrorLog(cfg.DataDir); err != nil {
		log.Printf("打开错误日志失败，崩溃信息只会写标准错误: %v", err)
	} else {
		defer func() { _ = helpers.CloseErrorLog() }()
	}

	if _, err := models.Open(cfg.DBFile); err != nil {
		helpers.Fatalf("启动", "打开数据库失败: %+v", errors.WithStack(err))
	}
	defer func() { _ = models.Close() }()

	// 挂载点聚合树常驻内存：启动时先建一次，之后由存储的增删改触发重建。
	// 树里只有挂载点的层级关系，真实目录与文件依然是每次列目录现读驱动。
	models.RefreshMountTree()

	// 一次性自救子命令：把某个账号的口令重设为指定值，然后退出。
	// 必须先连上库才能查到账号，所以放在这里而不是程序最前面。
	if len(os.Args) > 1 && os.Args[1] == "passwd" {
		// 这里不用 errors.WithStack：面向用户的 CLI 只该看到一句原因，
		// 不该被一大坨调用栈淹掉（wrapped 的上下文已经够定位问题了）。
		if err := resetPassword(os.Args[2:]); err != nil {
			log.Fatalf("重置口令失败: %v", err)
		}
		return
	}

	// 一次性索引子命令：`mocca index [挂载点]`。对每个存储递归扫描，
	// 在各目录生成 `.index.jsonl`；只做基础设施，海报/简介由外部工具写入 `.mocca`。
	if len(os.Args) > 1 && os.Args[1] == "index" {
		if err := runIndex(os.Args[2:]); err != nil {
			log.Fatalf("生成索引失败: %v", err)
		}
		return
	}

	// 连上库之后、服务启动之前：一个管理员都没有就用配置里的口令播种管理员。
	// 否则新装的服务无人能进管理后台，连存储都配不上。
	if created, err := models.EnsureAdmin(cfg.AdminPassword); err != nil {
		helpers.Fatalf("启动", "初始化管理员失败: %+v", errors.WithStack(err))
	} else if created {
		log.Printf("!!! 已创建管理员 %s / %s —— 请立刻登录并修改密码 !!!",
			models.DefaultAdminName, cfg.AdminPassword)
	}

	// 服务启动对每个启用存储先全量索引一次：文件监控只做增量刷新、凭空不会生成索引，
	// 首次挂载若没有 .index.jsonl 浏览端会一片空白。后台执行，失败不阻断启动。
	// 走 GoSafe：这活儿要遍历整棵目录树，单个存储的异常不该让进程消失。
	helpers.GoSafe("启动索引", func() {
		storages, lerr := models.ListStorages()
		if lerr != nil {
			log.Printf("启动索引失败: %v", lerr)
			return
		}
		for _, s := range storages {
			if s.Disabled {
				continue
			}
			n, serr := mediaindex.ScanStorage(s)
			if serr != nil {
				log.Printf("存储 %s 启动索引失败: %v", s.MountPath, serr)
				continue
			}
			if n > 0 {
				log.Printf("存储 %s 已索引 %d 个媒体文件", s.MountPath, n)
			}
		}
	})

	root := echo.New()

	// panic 兜底必须挂在最外层（第一个 Use）：中间件是洋葱模型，
	// 先注册的包住后面所有中间件与 handler，才接得住最里层抛上来的 panic。
	root.Use(middlewares.RecoverLog())
	// 请求体上限：防止超大请求把磁盘写满
	root.Use(middlewares.BodyLimit(middlewares.DefaultMaxBody))
	// 请求日志：排障第一手材料，慢请求会被标注
	root.Use(middlewares.RequestLogger(2 * time.Second))

	routes.SetupAPIRoutes(root)

	// 页面是可选能力，由构建标签决定：
	//   make full / go build ./        → 带上页面：浏览应用挂 /，后台挂 /admin/
	//   make api  / go build -tags noweb → web.Register 是空操作，不注册任何页面路由
	// 两种构建共用一个 main，差异全部收在 web 包里（见 web/embed.go 与 web/noweb.go）。
	if web.Embedded {
		if err := web.Register(root, "/admin"); err != nil {
			helpers.Fatalf("启动", "挂载页面失败: %+v", errors.WithStack(err))
		}
		log.Printf("浏览应用已挂载：%s/ （管理后台在 %s/admin/）", cfg.Addr, cfg.Addr)
	} else {
		log.Printf("纯 API 构建（-tags noweb）：不含管理后台，仅提供 REST API 与 passwd 子命令")
	}

	// echo v5 的 Echo 只提供 Start，没有 Shutdown；
	// 包一层自己的 http.Server，超时与优雅关闭就都能掌控。
	srv := &http.Server{
		Addr:        cfg.Addr,
		Handler:     root,
		ReadTimeout: 30 * time.Second,
		// 写超时必须留 0：流媒体是长连接，设了会把正在播放的视频掐断
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// 收到 SIGINT/SIGTERM 后优雅退出，等在途请求结束
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 媒体文件监控（FS Watch）：本地存储有文件增删改时增量刷新所在目录的 .index.jsonl。
	// **默认关闭**：inotify/kqueue 在大目录树上要登记每个子目录，代价不小，
	// 所以做成后台「全局选项」里的开关，只有在库里显式打开才拉起。
	if models.SettingBool(models.SettingFsWatch, false) {
		if err := mediaindex.Watch.Start(log.Printf); err != nil {
			log.Printf("启动文件监控失败: %v", err)
		}
		log.Printf("文件监控（FS Watch）已开启")
	} else {
		log.Printf("文件监控（FS Watch）未开启，可在后台「全局选项」打开")
	}

	helpers.GoSafe("http 服务", func() {
		log.Printf("mocca 监听 %s（数据目录 %s）", cfg.Addr, cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			helpers.Fatalf("服务", "服务异常退出: %+v", err)
		}
	})

	<-ctx.Done()
	log.Println("收到退出信号，正在优雅关闭…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("关闭超时: %+v", err)
	}
	// 连接池里的 SMB 会话要显式关掉，否则服务端会残留会话
	drivers.CloseAllSMB()
	_ = models.Close()
	log.Println("已退出")
}

// resetPassword 实现 `mocca passwd -u admin [-p 新口令]`。
//
// 这是「口令忘了 / 被写坏」时的唯一自救通道：库里只有不可逆的 bcrypt，
// 无法反推；而 EnsureAdmin 只在库中一个管理员都没有时才出手。
// 不传 -p 时从标准输入读一行，免得口令留在 shell 历史与 ps 输出里。
func resetPassword(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ContinueOnError)
	user := fs.String("u", models.DefaultAdminName, "要重置口令的账号")
	password := fs.String("p", "", "新口令；留空则从标准输入读一行")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pwd := *password
	if pwd == "" {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return errors.Wrap(err, "读取新口令")
		}
		pwd = strings.TrimRight(line, "\r\n")
	}
	if err := models.ResetPassword(*user, pwd); err != nil {
		return err
	}
	log.Printf("已重置账号 %q 的口令，请立刻用新口令登录并妥善保管", *user)
	return nil
}

// runIndex 实现 `mocca index [挂载点]`。不传挂载点则扫全部启用的存储；
// 传了则只扫那一个（按规范化后的挂载点精确匹配）。
func runIndex(args []string) error {
	mount := ""
	if len(args) > 0 {
		mount = models.NormalizeMountPath(args[0])
	}
	storages, err := models.ListStorages()
	if err != nil {
		return err
	}
	scanned, files := 0, 0
	for _, s := range storages {
		if s.Disabled {
			continue
		}
		if mount != "" && models.NormalizeMountPath(s.MountPath) != mount {
			continue
		}
		n, err := mediaindex.ScanStorage(s)
		if err != nil {
			log.Printf("存储 %s 索引失败: %v", s.MountPath, err)
			continue
		}
		scanned++
		files += n
		log.Printf("存储 %s 索引完成：%d 个媒体文件", s.MountPath, n)
	}
	if scanned == 0 {
		return errors.New("没有可扫描的存储（挂载点不存在或全部被禁用）")
	}
	log.Printf("共扫 %d 个存储、%d 个媒体文件", scanned, files)
	return nil
}
