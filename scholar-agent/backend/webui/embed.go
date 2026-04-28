package webui

import (
	"embed"
	"io/fs"
)

// dist 目录会在单文件打包前由 Makefile 同步前端构建产物。
//
//go:embed dist
var dist embed.FS

func DistFS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
