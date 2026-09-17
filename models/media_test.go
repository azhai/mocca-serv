package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func TestHiddenDirsForCoversAndThumbs(t *testing.T) {
	root := t.TempDir()

	if err := EnsureHiddenDirs(root); err != nil {
		t.Fatalf("EnsureHiddenDirs 失败: %v", err)
	}
	for _, want := range []string{
		filepath.Join(root, HiddenDirName, CoversSubDirName),
		filepath.Join(root, HiddenDirName, ThumbsSubDirName),
	} {
		if !dirExists(want) {
			t.Errorf("目录未创建: %s", want)
		}
	}

	// 落库的是相对路径：点号开头 = 隐藏，且不含数据目录，搬家后仍有效
	if got := CoverRelPath("a.jpg"); !strings.HasPrefix(got, ".") {
		t.Errorf("封面相对路径应以隐藏目录开头: %q", got)
	}
	if got := ThumbRelPath("b.jpg"); !strings.HasPrefix(got, ".") {
		t.Errorf("缩略图相对路径应以隐藏目录开头: %q", got)
	}
	if got := ResolveHiddenPath(root, CoverRelPath("a.jpg")); !strings.HasPrefix(got, root) {
		t.Errorf("还原绝对路径失败: %q", got)
	}
	// 重复调用幂等
	if err := EnsureHiddenDirs(root); err != nil {
		t.Errorf("重复创建应无错误: %v", err)
	}
}

func TestAvatarMustBePreset(t *testing.T) {
	u := &User{Username: "avatar-user"}

	if err := u.SetAvatar("03"); err != nil {
		t.Fatalf("预设头像应可选: %v", err)
	}
	if u.Avatar != "03" {
		t.Errorf("Avatar = %q, want 03", u.Avatar)
	}
	if err := u.SetAvatar("selfie.png"); err == nil {
		t.Error("非预设头像应被拒绝")
	}
	if u.Avatar != "03" {
		t.Errorf("非法设置不应改写原值，got %q", u.Avatar)
	}

	empty := &User{}
	if empty.AvatarOrDefault() != DefaultAvatar {
		t.Errorf("未设置头像时应回退默认 %q", DefaultAvatar)
	}
}

func TestCreateUserGetsDefaultAvatar(t *testing.T) {
	openTestDB(t)

	u := &User{Username: "neo", Role: RoleGeneral}
	if err := u.SetPassword("pw"); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(u); err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	if u.Avatar != DefaultAvatar {
		t.Errorf("新建账号应带默认头像，got %q", u.Avatar)
	}
}

func TestMediaMetaFieldsByKind(t *testing.T) {
	openTestDB(t)

	// 视频/音频：时长、封面、简介、多个作者
	video := &MediaMeta{
		Path:        "/movies/a.mp4",
		Kind:        MediaVideo,
		Size:        1024,
		Title:       "电影 A",
		Duration:    7325000,
		Cover:       CoverRelPath("a.jpg"),
		Description: "一部电影",
	}
	if err := CreateMedia(video, []string{"导演甲", "演员乙", "演员丙"}); err != nil {
		t.Fatalf("CreateMedia 失败: %v", err)
	}
	authors, err := ListAuthors(video.ID)
	if err != nil {
		t.Fatalf("ListAuthors 失败: %v", err)
	}
	if len(authors) != 3 || authors[0] != "导演甲" || authors[2] != "演员丙" {
		t.Errorf("作者列表/顺序不符: %v", authors)
	}

	got, err := GetMediaByPath("/movies/a.mp4")
	if err != nil {
		t.Fatalf("GetMediaByPath 失败: %v", err)
	}
	if got.Duration != 7325000 || got.Cover == "" || got.Description == "" {
		t.Errorf("音视频专属字段应保留: %+v", got)
	}

	// 图片：只有路径、大小、可选标题
	img := &MediaMeta{
		Path: "/photos/x.png", Kind: MediaImage, Size: 2048,
		Title:       "",             // 标题可空
		Duration:    999,            // 下列三项刻意传入，应被清空
		Cover:       "should-clear", //
		Description: "should-clear", //
	}
	if err = CreateMedia(img, nil); err != nil {
		t.Fatalf("CreateMedia 图片失败: %v", err)
	}
	if img.Duration != 0 || img.Cover != "" || img.Description != "" {
		t.Errorf("图片不应保留音视频字段: %+v", img)
	}
	if got2, _ := GetMediaByPath("/photos/x.png"); got2 == nil || got2.Size != 2048 {
		t.Errorf("图片共有字段应保留: %+v", got2)
	}

	// 按作者反查
	found, err := FindMediaByAuthor("导演甲")
	if err != nil || len(found) != 1 || found[0].Path != "/movies/a.mp4" {
		t.Errorf("按作者反查失败: %+v, err=%v", found, err)
	}
}

func TestMediaRejectsBadInput(t *testing.T) {
	openTestDB(t)

	if err := CreateMedia(&MediaMeta{Path: "/x", Kind: 99}, nil); err == nil {
		t.Error("非法媒体类型应报错")
	}
	if err := CreateMedia(&MediaMeta{Kind: MediaVideo}, nil); err == nil {
		t.Error("空路径应报错")
	}
}

func TestCommentAndDanmaku(t *testing.T) {
	openTestDB(t)

	// 评论：不限字数，时刻恒为 0
	long := strings.Repeat("这", 500)
	c := &Comment{UserID: 1, Path: "/m/a.mp4", Type: CommentTypeComment, Offset: 12345, Content: long}
	if err := AddComment(c); err != nil {
		t.Fatalf("长评论应被接受: %v", err)
	}
	if c.Offset != 0 {
		t.Errorf("评论时刻应被归零，got %d", c.Offset)
	}

	// 弹幕：正好 50 字放行，51 字拒绝（按码点算，不是字节）
	ok := &Comment{UserID: 1, Path: "/m/a.mp4", Type: CommentTypeDanmaku, Offset: 60000, Content: strings.Repeat("弹", MaxDanmakuLength)}
	if err := AddComment(ok); err != nil {
		t.Fatalf("50 字弹幕应被接受: %v", err)
	}
	if ok.Offset != 60000 {
		t.Errorf("弹幕时刻应保留，got %d", ok.Offset)
	}
	tooLong := &Comment{UserID: 1, Path: "/m/a.mp4", Type: CommentTypeDanmaku, Offset: 0, Content: strings.Repeat("弹", MaxDanmakuLength+1)}
	if err := AddComment(tooLong); err == nil {
		t.Error("超过 50 字的弹幕应被拒绝")
	}
	negOffset := &Comment{UserID: 1, Path: "/m/a.mp4", Type: CommentTypeDanmaku, Offset: -1, Content: "x"}
	if err := AddComment(negOffset); err == nil {
		t.Error("负时刻弹幕应被拒绝")
	}
	if err := AddComment(&Comment{Path: "/m/a.mp4", Type: 7, Content: "x"}); err == nil {
		t.Error("未知类型应被拒绝")
	}
	if err := AddComment(&Comment{Path: "/m/a.mp4", Type: CommentTypeComment, Content: "   "}); err == nil {
		t.Error("空内容应被拒绝")
	}

	// 弹幕按时刻正序返回，播放器直接按时间轴喂
	late := &Comment{UserID: 1, Path: "/m/a.mp4", Type: CommentTypeDanmaku, Offset: 120000, Content: "后面"}
	if err := AddComment(late); err != nil {
		t.Fatal(err)
	}
	dm, err := ListDanmaku("/m/a.mp4")
	if err != nil {
		t.Fatalf("ListDanmaku 失败: %v", err)
	}
	if len(dm) != 2 || dm[0].Offset != 60000 || dm[1].Offset != 120000 {
		t.Errorf("弹幕应按时刻排序: %+v", dm)
	}

	comments, err := ListComments("/m/a.mp4")
	if err != nil || len(comments) != 1 {
		t.Errorf("评论应单独成列: %d 条, err=%v", len(comments), err)
	}

	// 不能删别人的评论
	if err = DeleteComment(comments[0].ID, 999); err == nil {
		t.Error("删除他人评论应被拒绝")
	}
	if err = DeleteComment(comments[0].ID, 1); err != nil {
		t.Errorf("删除自己的评论失败: %v", err)
	}
}

func TestFavoriteLifecycle(t *testing.T) {
	openTestDB(t)

	f := &Favorite{UserID: 7, Path: "/m/a.mp4", Name: "a.mp4", Kind: MediaVideo, Thumb: ThumbRelPath("a.jpg")}
	if err := AddFavorite(f); err != nil {
		t.Fatalf("AddFavorite 失败: %v", err)
	}
	// 重复收藏不产生新行
	if err := AddFavorite(&Favorite{UserID: 7, Path: "/m/a.mp4"}); err != nil {
		t.Fatalf("重复收藏不应报错: %v", err)
	}
	all, err := ListFavorites(7)
	if err != nil {
		t.Fatalf("ListFavorites 失败: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("收藏应去重，got %d 条", len(all))
	}

	ok, err := IsFavorited(7, "/m/a.mp4")
	if err != nil || !ok {
		t.Errorf("IsFavorited = %v, err=%v, want true", ok, err)
	}
	if ok, _ = IsFavorited(7, "/m/b.mp4"); ok {
		t.Error("未收藏的应返回 false")
	}

	if err = RemoveFavorite(7, "/m/a.mp4"); err != nil {
		t.Fatalf("RemoveFavorite 失败: %v", err)
	}
	if all, _ = ListFavorites(7); len(all) != 0 {
		t.Errorf("取消收藏后应为空，got %d", len(all))
	}

	// 缺字段直接报错
	if err = AddFavorite(&Favorite{UserID: 0, Path: "/x"}); err == nil {
		t.Error("缺 user_id 应报错")
	}
}
