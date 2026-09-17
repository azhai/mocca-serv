package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/azhai/mocca/middlewares"
	"github.com/azhai/mocca/models"
	"github.com/labstack/echo/v5"
)

// ListFavorites 我的收藏。
func ListFavorites(c *echo.Context) error {
	all, err := models.ListFavorites(middlewares.UserID(c))
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取收藏失败")
	}
	return helpers.OK(c, all)
}

// AddFavorite 收藏。重复收藏不会产生第二条记录。
func AddFavorite(c *echo.Context) error {
	var req struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Kind  int    `json:"kind"`
		Thumb string `json:"thumb"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	f := &models.Favorite{
		UserID: middlewares.UserID(c),
		Path:   req.Path,
		Name:   req.Name,
		Kind:   req.Kind,
		Thumb:  req.Thumb,
	}
	if err := models.AddFavorite(f); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "收藏失败")
	}
	return helpers.OK(c, nil)
}

// RemoveFavorite 取消收藏。
func RemoveFavorite(c *echo.Context) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if err := models.RemoveFavorite(middlewares.UserID(c), req.Path); err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "取消收藏失败")
	}
	return helpers.OK(c, nil)
}

// AddComment 发表评论或发弹幕。type=0 评论（不限字数、时刻为 0），
// type=1 弹幕（≤50 字、offset 为出现时刻）。
func AddComment(c *echo.Context) error {
	uid := middlewares.UserID(c)
	if uid == 0 {
		return helpers.Fail(c, helpers.CodeUnauthorized, "未登录")
	}
	var req struct {
		Path    string `json:"path"`
		Type    int    `json:"type"`
		Offset  int    `json:"offset"`
		Content string `json:"content"`
	}
	if err := c.Bind(&req); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	cm := &models.Comment{
		UserID:  uid,
		Path:    req.Path,
		Type:    req.Type,
		Offset:  req.Offset,
		Content: req.Content,
	}
	if err := models.AddComment(cm); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	return helpers.OK(c, cm)
}

// ListComments 某媒体的评论。
func ListComments(c *echo.Context) error {
	path := c.QueryParam("path")
	if path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	rows, err := models.ListComments(path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取评论失败")
	}
	return helpers.OK(c, rows)
}

// ListDanmaku 某媒体的弹幕，按出现时刻排序，播放器直接按时间轴取用。
func ListDanmaku(c *echo.Context) error {
	path := c.QueryParam("path")
	if path == "" {
		return helpers.Fail(c, helpers.CodeBadRequest, "缺少 path")
	}
	rows, err := models.ListDanmaku(path)
	if err != nil {
		return helpers.Fail(c, helpers.CodeInternal, "读取弹幕失败")
	}
	return helpers.OK(c, rows)
}

// DeleteComment 删除自己发的评论/弹幕。
func DeleteComment(c *echo.Context) error {
	var req struct {
		ID uint `json:"id"`
	}
	if err := c.Bind(&req); err != nil || req.ID == 0 {
		return helpers.Fail(c, helpers.CodeBadRequest, "请求参数有误")
	}
	if err := models.DeleteComment(req.ID, middlewares.UserID(c)); err != nil {
		return helpers.Fail(c, helpers.CodeBadRequest, err.Error())
	}
	return helpers.OK(c, nil)
}
