package handlers

import (
	"strings"
	"testing"
)

func TestParseProcMounts(t *testing.T) {
	sample := `rootfs / rootfs rw 0 0
/dev/sda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
/dev/sdb1 /media/MyUSB vfat rw 0 0
/dev/sdc1 /mnt/data exfat rw 0 0
tmpfs /run tmpfs rw 0 0
`
	mounts, err := parseProcMounts(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 6 {
		t.Fatalf("解析行数 = %d，want 6", len(mounts))
	}
	if mounts[1].dev != "/dev/sda1" || mounts[1].mp != "/" || mounts[1].fs != "ext4" {
		t.Fatalf("第二行解析错：%#v", mounts[1])
	}
}

func TestCandidatesFromProc(t *testing.T) {
	sample := `/dev/sda1 / ext4 rw 0 0
/dev/sda2 /boot ext4 rw 0 0
/dev/sdb1 /media/MyUSB vfat rw 0 0
/dev/sdc1 /mnt/data exfat rw 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
`
	mounts, _ := parseProcMounts(strings.NewReader(sample))
	list := candidatesFromProc(mounts)
	// 应只剩 /media/MyUSB 与 /mnt/data（/boot 在 sysPrefixes 内被排除）
	if len(list) != 2 {
		t.Fatalf("候选数 = %d，want 2，got %#v", len(list), list)
	}
	for _, c := range list {
		if c.Path != "/media/MyUSB" && c.Path != "/mnt/data" {
			t.Fatalf("出现非预期候选 %s", c.Path)
		}
	}
	// 外接应排在前
	if !list[0].Removable {
		t.Errorf("外接设备应排前，got %#v", list[0])
	}
}

func TestStripPartition(t *testing.T) {
	cases := map[string]string{
		"sdb1":      "sdb",
		"nvme0n1p1": "nvme0n1",
		"mmcblk0p1": "mmcblk0",
		"sda":       "sda",
		"vda1":      "vda",
	}
	for in, want := range cases {
		if got := stripPartition(in); got != want {
			t.Errorf("stripPartition(%q) = %q，want %q", in, got, want)
		}
	}
}
