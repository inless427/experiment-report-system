package main

import (
	"archive/zip"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const uploadRoot = "data"

var unsafeNameRE = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+`)

// safeName 把任意字符串转成可作为目录或文件名的安全形式。
func safeName(name string) string {
	name = strings.TrimSpace(unsafeNameRE.ReplaceAllString(name, "_"))
	if name == "" {
		return "unnamed"
	}
	return name
}

// addFileToZip 把 path 文件以 nameInZip 写入 zip writer。
func addFileToZip(zw *zip.Writer, path, nameInZip string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	w, err := zw.Create(nameInZip)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// envStr 从环境变量读取字符串，缺省时返回 fallback。
func envStr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

// parseIDParam 把 path 参数解析为 int64。
func parseIDParam(value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// uploadDirFor 拼出某课程某实验项目的实际目录。
func uploadDirFor(courseFolder, labFolder string) string {
	return filepath.Join(uploadRoot, courseFolder, labFolder)
}

// urlEscape 对文件名做 RFC 5987 编码，配合 Content-Disposition 的 filename*=UTF-8'' 使用。
func urlEscape(s string) string {
	return url.PathEscape(s)
}
