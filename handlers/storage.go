package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// StorageReq 挂载点请求。Addition 是驱动私有配置的 JSON 文本，
// 本地存储形如 {"root_folder_path":"/mnt/media"}。
type StorageReq struct {
	ID        uint   `json:"id"`
	MountPath string `json:"mount_path"`
	Order     int    `json:"order"`
	Driver    string `json:"driver"`
	Addition  string `json:"addition"`
	Disabled  bool   `json:"disabled"`
}

// ListStorage 列出全部挂载点。
func ListStorage(c *echo.Context) error {
	all, err := models.ListStorages()
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取存储失败")
	}
	return helpers.OK(c, all)
}

// CreateStorage 新增挂载点。
func CreateStorage(c *echo.Context) error {
	var req StorageReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if req.MountPath == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "挂载点不能为空")
	}
	driver := req.Driver
	if driver == "" {
		driver = "Local"
	}
	s := &models.Storage{
		MountPath: req.MountPath,
		Order:     req.Order,
		Driver:    driver,
		Addition:  req.Addition,
		Disabled:  req.Disabled,
	}
	if err := models.CreateStorage(s); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "创建失败，挂载点可能已存在")
	}
	return helpers.OK(c, s)
}

// UpdateStorage 修改挂载点。
func UpdateStorage(c *echo.Context) error {
	var req StorageReq
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	s, err := models.GetStorageByID(req.ID)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "挂载点不存在")
	}
	if req.MountPath != "" {
		s.MountPath = req.MountPath
	}
	if req.Driver != "" {
		s.Driver = req.Driver
	}
	if req.Addition != "" {
		s.Addition = req.Addition
	}
	s.Order = req.Order
	s.Disabled = req.Disabled
	if err = models.UpdateStorage(s); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "更新失败")
	}
	return helpers.OK(c, s)
}

// DeleteStorage 删除挂载点。
func DeleteStorage(c *echo.Context) error {
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.Bind(&req); err != nil || req.ID == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if err := models.DeleteStorage(req.ID); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "删除失败")
	}
	return helpers.OK(c, nil)
}

// SaveMeta 写入/更新媒体元数据（含多个作者）。
func SaveMeta(c *echo.Context) error {
	var req struct {
		Path        string   `json:"path"`
		Kind        int      `json:"kind"`
		Size        int64    `json:"size"`
		Title       string   `json:"title"`
		Duration    int      `json:"duration"`
		Cover       string   `json:"cover"`
		Description string   `json:"description"`
		Authors     []string `json:"authors"`
	}
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if !models.IsValidMediaKind(req.Kind) {
		return helpers.Fail(c, helpers.CodeBadRequest, "媒体类型不合法")
	}

	// 有则更新、无则新建
	meta, err := models.GetMediaByPath(req.Path)
	if err != nil {
		meta = &models.MediaMeta{Path: req.Path, Kind: req.Kind}
	}
	meta.Kind = req.Kind
	meta.Size = req.Size
	meta.Title = req.Title
	meta.Duration = req.Duration
	meta.Cover = req.Cover
	meta.Description = req.Description

	if meta.ID == 0 {
		if err = models.CreateMedia(meta, req.Authors); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "创建元数据失败")
		}
	} else {
		if err = models.UpdateMedia(meta); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "更新元数据失败")
		}
		if err = models.SetAuthors(meta.ID, req.Authors); err != nil {
			return helpers.Fail(c, helpers.CodeInternal, "更新作者失败")
		}
	}
	return helpers.OK(c, meta)
}

// GetMeta 读取媒体元数据。
func GetMeta(c *echo.Context) error {
	path := c.QueryParam("path")
	if path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	meta, err := models.GetMediaByPath(path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeNotFound, "尚无元数据")
	}
	authors, _ := models.ListAuthors(meta.ID)
	return helpers.OK(c, map[string]any{"meta": meta, "authors": authors})
}
