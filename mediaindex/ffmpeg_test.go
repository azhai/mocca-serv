package mediaindex

import (
	"os"
	"os/exec"
	"testing"
)

// TestFFmpegBinResolvesWithoutHomebrewInPATH 模拟服务被 launchd 类守护进程以极简
// PATH 拉起的场景（仅 /usr/bin:/bin:/usr/sbin:/sbin），此时 /opt/homebrew/bin 与
// /usr/local/bin 都不在 PATH 里。ffmpegBin 必须靠候选绝对路径兜底找到 ffmpeg，
// 否则截图 / HLS 切分会回「未找到 ffmpeg」。
func TestFFmpegBinResolvesWithoutHomebrewInPATH(t *testing.T) {
	if _, err := exec.LookPath("/usr/local/bin/ffmpeg"); err != nil {
		t.Skip("/usr/local/bin/ffmpeg 不存在，跳过（本机未装 ffmpeg）")
	}

	old := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", old) })

	// 极简守护进程 PATH
	os.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	p, err := ffmpegBin()
	if err != nil {
		t.Fatalf("极简 PATH 下仍应解析到 ffmpeg: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("ffmpegBin 返回的路径不可访问: %s → %v", p, err)
	}
	t.Logf("极简 PATH 下解析到 ffmpeg: %s", p)
}
