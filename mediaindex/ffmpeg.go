package mediaindex

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pkg/errors"
)

// ffmpegBin 返回可用的 ffmpeg 可执行文件绝对路径。
//
// 先按 PATH 找（交互式/登录 shell，以及经 path_helper 的守护进程都覆盖）；
// 失败再回退到一组合法安装位置。服务常被 gorch / launchd 类守护进程以极简 PATH
// （仅 /usr/bin:/bin:/usr/sbin:/sbin）拉起，那种上下文 neither /opt/homebrew/bin
// nor /usr/local/bin 都不在 PATH 里，纯靠 PATH 会找不到——这里兜底把它们直接写死。
func ffmpegBin() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	candidates := []string{
		"/usr/local/bin/ffmpeg",
		"/opt/homebrew/bin/ffmpeg",
		"/opt/homebrew/sbin/ffmpeg",
		"/usr/bin/ffmpeg",
		"/bin/ffmpeg",
		"/opt/local/bin/ffmpeg",        // MacPorts
		"/snap/bin/ffmpeg",             // Ubuntu snap
		"/usr/local/ffmpeg/bin/ffmpeg", // 静态构建常见落点
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs, nil
			}
		}
	}
	return "", errors.New("未找到 ffmpeg，请先安装并加入 PATH（或放到 /usr/local/bin、/opt/homebrew/bin 等已知位置）")
}
