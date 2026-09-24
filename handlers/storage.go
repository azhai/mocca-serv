package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// StorageReq 挂载点请求。Addition 是驱动私有配置的 JSON 文本，
// 本地存储形如 {"root_folder_path":"/mnt/media","meta_dir":"/mnt/media/.mocca"}，
// meta_dir 为可选项，缺省由驱动推算（内容根下的 .mocca）。
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
