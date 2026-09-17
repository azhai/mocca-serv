//go:build noweb

package handlers

import (
	"github.com/azhai/mocca/helpers"
	"github.com/labstack/echo/v5"
)

// SaveCover 在纯 API 构建里不可用：封面图是管理后台用 canvas 画的，
// 而后台（连同这个处理器）已被编译期剥离。
//
// 保留同名处理器是为了让 routes 在两种构建下都能原样编译；
// 命中时明确说明原因，而不是抛一个查不出所以然的 404。
func SaveCover(c *echo.Context) error {
	return helpers.Fail(c, helpers.CodeNotFound,
		"当前为纯 API 构建（-tags noweb），未包含封面图制作；请改用带管理后台的构建")
}
