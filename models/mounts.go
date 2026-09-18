package models

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// NormalizeMountPath 规范化挂载点：开头补斜线、去掉末尾斜线、合并重复斜线、
// 清掉 `.` 与 `..`。用户写 `media`、`media/`、`/media//` 存下来的都是 `/media`；
// 根仍然是 `/`。
//
// 必须在**保存时**就规范化：库里存 `media/` 的话，前缀匹配、聚合树、
// 客户端拿到的路径三种口径会各不相同，后面每次比较都要各自兜一遍。
func NormalizeMountPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// LessName 展示用的名字比较：先忽略大小写的字母序，再按原始字节序兜底。
// 直接比字符串是字节序，`Zebra` 会排在 `apple` 前面 —— 对用户可见的列表不自然；
// 兜底那一次是为了同名字（仅大小写不同）也有稳定顺序。
func LessName(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

// mountTree 挂载点聚合树：**只存挂载点**，按层级组织。
//
//   - children[目录] = 该层要展示的子节点名。它可能是挂载点本身，也可能是
//     通往更深挂载点的中间路径段（只挂了 /nas/movies 时，`/` 这一层就有 `nas`）；
//   - mounts[路径] = 该路径本身就是一个挂载点。
//
// 文件系统里的真实目录与文件**不进这棵树**：它们每次列目录时现读驱动，
// 所以存储内容怎么变都不需要失效，只有挂载点增删改才会重建。
type mountTree struct {
	children map[string][]string
	mounts   map[string]bool
}

var (
	treeMu sync.RWMutex
	tree   *mountTree
)

// MountChildren 返回 path 这一层要虚拟展示的挂载点节点（已按名字排序）。
// 纯挂载点层级信息，不含任何真实目录与文件。
func MountChildren(p string) []string {
	return loadMountTree().children[NormalizeMountPath(p)]
}

// MountCount 当前生效（未禁用）的挂载点数量，主要用于日志与测试断言。
func MountCount() int {
	return len(loadMountTree().mounts)
}

// RefreshMountTree 立即重建挂载点聚合树（启动时预热，或需要强制刷新时调用）。
func RefreshMountTree() {
	t := buildMountTree()
	treeMu.Lock()
	tree = t
	treeMu.Unlock()
}

// InvalidateMountTree 标记失效，下次读时惰性重建。
// 挂载点增删改之后必须调用，否则列表还停在旧的挂载点集合上。
func InvalidateMountTree() {
	treeMu.Lock()
	tree = nil
	treeMu.Unlock()
}

// loadMountTree 取聚合树，没有就现建。加写锁后再确认一次，避免并发重复重建。
func loadMountTree() *mountTree {
	treeMu.RLock()
	t := tree
	treeMu.RUnlock()
	if t != nil {
		return t
	}
	treeMu.Lock()
	defer treeMu.Unlock()
	if tree == nil {
		tree = buildMountTree()
	}
	return tree
}

func buildMountTree() *mountTree {
	t := &mountTree{children: map[string][]string{}, mounts: map[string]bool{}}
	storages, err := ListStorages()
	if err != nil {
		// 读库失败就给一棵空树：列目录本身还会再报错，不该在这里崩
		return t
	}
	level := map[string]map[string]bool{} // 目录 → 该层子节点名集合
	for _, s := range storages {
		if s.Disabled {
			continue // 禁用的挂载点不参与匹配，也不该出现在聚合树里
		}
		mount := NormalizeMountPath(s.MountPath)
		t.mounts[mount] = true
		if mount == "/" {
			continue // 挂在根上的存储本身就是根，没有虚拟节点可加
		}
		cur := "/"
		for seg := range strings.SplitSeq(strings.TrimPrefix(mount, "/"), "/") {
			if seg == "" {
				continue
			}
			if level[cur] == nil {
				level[cur] = map[string]bool{}
			}
			level[cur][seg] = true
			cur = path.Join(cur, seg)
		}
	}
	for dir, set := range level {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool { return LessName(names[i], names[j]) })
		t.children[dir] = names
	}
	return t
}
