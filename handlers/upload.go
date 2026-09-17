package handlers

import (
	"io"
	"mime/multipart"
	"path"
	"path/filepath"

	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
	"github.com/pkg/errors"
)

// UploadResult 单个文件的上传结果，前端据此把每条进度条标成完成或失败。
type UploadResult struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Message string `json:"message,omitempty"`
}

// Upload 批量上传。
//
// 设计取舍：一个请求只处理表单里的多个文件，但**前端应当一个文件发一次请求**
// （见 UploadOne）——这样浏览器 XHR 的 upload.onprogress 天然给出「每个文件一条进度」；
// 若把 N 个文件塞进一个请求，只能拿到整体进度，无法分文件显示。
// 这里保留批量入口是为了兼容一次性提交的场景。
func Upload(c *echo.Context) error {
	dir := c.QueryParam("path")
	form, err := c.MultipartForm()
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求不是合法的表单上传")
	}
	files := form.File["files"]
	if len(files) == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "没有文件")
	}

	results := make([]UploadResult, 0, len(files))
	for _, fh := range files {
		size, err := saveOne(dir, fh)
		r := UploadResult{Name: fh.Filename, Size: size}
		if err != nil {
			r.Message = err.Error()
		}
		results = append(results, r)
	}
	return helpers.OK(c, results)
}

// UploadOne 单文件上传，前端逐文件并发调用以获得独立进度条。
func UploadOne(c *echo.Context) error {
	dir := c.QueryParam("path")
	form, err := c.MultipartForm()
	if err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求不是合法的表单上传")
	}
	files := form.File["file"]
	if len(files) == 0 {
		files = form.File["files"]
	}
	if len(files) == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "没有文件")
	}
	size, err := saveOne(dir, files[0])
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, err.Error())
	}
	return helpers.OK(c, UploadResult{Name: files[0].Filename, Size: size})
}

// saveOne 把一个表单文件写入存储；文件名只取基名，挡掉 ../ 之类的路径穿越。
func saveOne(dir string, fh *multipart.FileHeader) (int64, error) {
	name := filepath.Base(fh.Filename)
	if name == "" || name == "." || name == "/" {
		return 0, errors.New("文件名不合法")
	}

	drv, relDir, err := openStorage(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = drv.Close() }()

	src, err := fh.Open()
	if err != nil {
		return 0, errors.WithStack(err)
	}
	defer func() { _ = src.Close() }()

	dst, err := drv.Create(path.Join(relDir, name))
	if err != nil {
		return 0, err
	}
	defer func() { _ = dst.Close() }()

	written, err := io.Copy(dst, src)
	if err != nil {
		return written, errors.WithStack(err)
	}
	return written, nil
}
