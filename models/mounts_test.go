package models

import (
	"slices"
	"testing"
)

func TestNormalizeMountPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "/"},
		{"   ", "/"},
		{"/", "/"},
		{"media", "/media"},
		{"media/", "/media"},
		{"/media/", "/media"},
		{"  /media  ", "/media"},
		{"//media//sub/", "/media/sub"},
		{"/media/./sub/", "/media/sub"},
		{"/media/../nas", "/nas"},
	}
	for _, c := range cases {
		if got := NormalizeMountPath(c.in); got != c.want {
			t.Errorf("NormalizeMountPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// assertChildren 断言某一层的虚拟子节点（顺序敏感：聚合树里就排好序了）。
func assertChildren(t *testing.T, dir string, want []string) {
	t.Helper()
	if got := MountChildren(dir); !slices.Equal(got, want) {
		t.Errorf("MountChildren(%q) = %v, want %v", dir, got, want)
	}
}

// TestMountTreeKeepsOnlyMountPoints 聚合树只存挂载点的层级：
// 真实目录与文件不进树，但「通往更深挂载点的中间路径」要自己长出来。
func TestMountTreeKeepsOnlyMountPoints(t *testing.T) {
	openTestDB(t)

	media := &Storage{MountPath: "media/", Driver: "Local"}
	if err := CreateStorage(media); err != nil {
		t.Fatalf("建挂载点失败: %v", err)
	}
	// 保存时就该被规范化，库里不该留下 `media/`
	if media.MountPath != "/media" {
		t.Fatalf("保存时未规范化挂载点: %q", media.MountPath)
	}
	deep := &Storage{MountPath: "/nas/movies/", Driver: "Local"}
	if err := CreateStorage(deep); err != nil {
		t.Fatalf("建深层挂载点失败: %v", err)
	}
	// 禁用的挂载点不该出现在树里
	if err := CreateStorage(&Storage{MountPath: "/gone", Driver: "Local", Disabled: true}); err != nil {
		t.Fatalf("建禁用挂载点失败: %v", err)
	}

	assertChildren(t, "/", []string{"media", "nas"})
	// /nas 自己不是挂载点，但它是通往 /nas/movies 的必经中间层
	assertChildren(t, "/nas", []string{"movies"})
	// 挂载点那一层没有虚拟子节点：树里不存文件系统的目录
	assertChildren(t, "/media", nil)
	assertChildren(t, "/gone", nil)
	// 路径形态不影响查树
	assertChildren(t, "nas/", []string{"movies"})
	if n := MountCount(); n != 2 {
		t.Errorf("生效挂载点数 = %d, want 2", n)
	}

	// 改名后顺序跟着变：忽略大小写的字母序（Media 在 nas 之前）
	media.MountPath = "Media/"
	if err := UpdateStorage(media); err != nil {
		t.Fatalf("改挂载点失败: %v", err)
	}
	assertChildren(t, "/", []string{"Media", "nas"})

	// 删除后立刻消失：树是内存缓存，忘了失效就会一直停在旧集合上
	if err := DeleteStorage(media.ID); err != nil {
		t.Fatalf("删挂载点失败: %v", err)
	}
	assertChildren(t, "/", []string{"nas"})
	if n := MountCount(); n != 1 {
		t.Errorf("删除后生效挂载点数 = %d, want 1", n)
	}
}

func TestLessName(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"apple", "Banana", true},  // 忽略大小写：a < b
		{"Banana", "apple", false}, // 大写不该把 apple 挤到后面
		{"a", "a", false},
		{"A", "a", true}, // 仅大小写不同时按原始字节序兜底
	}
	for _, c := range cases {
		if got := LessName(c.a, c.b); got != c.want {
			t.Errorf("LessName(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
