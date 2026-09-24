package handlers

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
)

// MountCandidate 是扫描到的疑似外接设备挂载点，可直接填进存储的 root_folder_path。
type MountCandidate struct {
	Path      string `json:"path"`      // 候选根目录（绝对路径）
	Source    string `json:"source"`    // 设备，如 /dev/sdb1
	Fstype    string `json:"fstype"`    // 文件系统类型
	Removable bool   `json:"removable"` // 是否为非系统盘（疑似外接）
	Label     string `json:"label"`     // 友好名：挂载点末段
}

// ScanMounts 自动发现外接设备根目录。仅管理员可用；找不到就返回空列表，
// 由前端退回手工填写。
func ScanMounts(c *echo.Context) error {
	list, err := scanExternalMounts()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "扫描挂载点失败："+err.Error())
	}
	if list == nil {
		list = []MountCandidate{}
	}
	return helpers.OK(c, list)
}

// scanExternalMounts 按操作系统挑实现。
func scanExternalMounts() ([]MountCandidate, error) {
	switch runtime.GOOS {
	case "linux":
		f, err := os.Open("/proc/self/mounts")
		if err != nil {
			return nil, err
		}
		defer f.Close()
		mounts, err := parseProcMounts(f)
		if err != nil {
			return nil, err
		}
		return candidatesFromProc(mounts), nil
	case "darwin":
		return scanDarwinMounts()
	default:
		// Windows 等暂不支持自动发现，返回空（走手工填写）。
		return []MountCandidate{}, nil
	}
}

// rawMount 是解析后的单行挂载记录。
type rawMount struct {
	dev, mp, fs string
}

// parseProcMounts 解析 /proc/mounts 的一行：dev mountpoint fstype opts ...
func parseProcMounts(r io.Reader) ([]rawMount, error) {
	var out []rawMount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		out = append(out, rawMount{dev: f[0], mp: f[1], fs: f[2]})
	}
	return out, sc.Err()
}

// sysPrefixes 这些是系统挂载点，外接设备不会挂在这里。
var sysPrefixes = []string{"/proc", "/sys", "/dev", "/run", "/boot", "/efi"}

// candidatesFromProc 从 /proc/mounts 记录里筛出疑似外接设备根目录。
func candidatesFromProc(mounts []rawMount) []MountCandidate {
	rootDev := ""
	for _, m := range mounts {
		if m.mp == "/" {
			rootDev = m.dev
		}
	}
	var out []MountCandidate
	for _, m := range mounts {
		if !strings.HasPrefix(m.dev, "/dev/") {
			continue // 只认块设备；tmpfs/proc/overlay 等直接排除
		}
		if m.mp == "/" || underSystem(m.mp) {
			continue
		}
		rem, err := removableOf(m.dev)
		if err != nil {
			rem = m.dev != rootDev // /sys 读不到时兜底：不是根盘即疑似外接
		}
		out = append(out, MountCandidate{
			Path: m.mp, Source: m.dev, Fstype: m.fs,
			Removable: rem, Label: filepath.Base(m.mp),
		})
	}
	sortCandidates(out)
	return out
}

// underSystem 判断挂载点是否落在系统目录下。
func underSystem(mp string) bool {
	for _, p := range sysPrefixes {
		if mp == p || strings.HasPrefix(mp, p+"/") {
			return true
		}
	}
	return false
}

// removableOf 通过 /sys 判断块设备是否可移除（U 盘/移动硬盘）。
// dev 形如 /dev/sdb1、/dev/nvme0n1p1、/dev/mmcblk0p1。
func removableOf(dev string) (bool, error) {
	name := strings.TrimPrefix(dev, "/dev/")
	base := stripPartition(name)
	for _, p := range []string{
		"/sys/class/block/" + base + "/removable",
		"/sys/block/" + base + "/removable",
	} {
		b, err := os.ReadFile(p)
		if err == nil {
			return strings.TrimSpace(string(b)) == "1", nil
		}
	}
	return false, fmt.Errorf("无 removable 标记：%s", dev)
}

// stripPartition 去掉设备名末尾的分区号，得到块设备名。
// sdb1 -> sdb，nvme0n1p1 -> nvme0n1，mmcblk0p1 -> mmcblk0。
func stripPartition(name string) string {
	name = strings.TrimRight(name, "0123456789")
	return strings.TrimSuffix(name, "p")
}

// scanDarwinMounts 解析 macOS 的 `mount` 输出，只留 /Volumes 下的外接盘。
func scanDarwinMounts() ([]MountCandidate, error) {
	out, err := exec.Command("mount").Output()
	if err != nil {
		return nil, err
	}
	rootDev := ""
	var raws []rawMount
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, " on ")
		if idx < 0 {
			continue
		}
		dev := line[:idx]
		rest := line[idx+4:]
		p2 := strings.Index(rest, " (")
		if p2 < 0 {
			continue
		}
		mp := rest[:p2]
		if mp == "/" {
			rootDev = dev
			continue
		}
		fs := ""
		inner := rest[p2+2:]
		if end := strings.Index(inner, ","); end >= 0 {
			fs = inner[:end]
		} else {
			fs = strings.TrimSuffix(inner, ")")
		}
		raws = append(raws, rawMount{dev: dev, mp: mp, fs: fs})
	}
	var out2 []MountCandidate
	for _, m := range raws {
		if !strings.HasPrefix(m.mp, "/Volumes/") || m.mp == "/Volumes" {
			continue
		}
		out2 = append(out2, MountCandidate{
			Path: m.mp, Source: m.dev, Fstype: m.fs,
			Removable: m.dev != rootDev, Label: filepath.Base(m.mp),
		})
	}
	sortCandidates(out2)
	return out2, nil
}

// sortCandidates 外接设备排前，再按路径字典序。
func sortCandidates(list []MountCandidate) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Removable != list[j].Removable {
			return list[i].Removable
		}
		return list[i].Path < list[j].Path
	})
}
