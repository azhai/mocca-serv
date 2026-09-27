package models

// 媒体类型。
//
// 取值必须与 APP 端 `MediaKind` 逐一对齐：
// dir=0 / unknown=1 / video=2 / audio=3 / text=4 / image=5。
// 绝不能用 iota 顺排——错位会让 APP 把视频判为「未知」（不可播放）、
// 把图片判为「音频」，而且这种错位是静默的，不报错、只是行为不对。
const (
	MediaDir     = 0
	MediaUnknown = 1
	MediaVideo   = 2
	MediaAudio   = 3
	MediaText    = 4
	MediaImage   = 5
)

// IsValidMediaKind 是否合法的媒体类型。
func IsValidMediaKind(kind int) bool {
	return kind == MediaVideo || kind == MediaAudio || kind == MediaImage
}
