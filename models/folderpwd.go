package models

import (
	"path"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
)

// FolderPwdPrefix settings 表里存放「目录密码」的键前缀。
//
// 复用 settings 表而不是新开一张：一个目录至多一个密码，key 天然唯一，
// 也省掉一次 schema 迁移。
const FolderPwdPrefix = "folder_pwd:"

// normalizeFolder 统一目录路径：补前导斜杠、清理冗余段、去掉尾斜杠。
func normalizeFolder(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// SetFolderPassword 给目录设密码。与登录同样的红线：
// 库里只放 bcrypt 不可逆哈希，绝不存明文。
func SetFolderPassword(dir, secret string) error {
	dir = normalizeFolder(dir)
	if secret == "" {
		return ClearFolderPassword(dir)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return errors.Wrap(err, "hash folder password")
	}
	return SetSetting(&Setting{Key: FolderPwdPrefix + dir, Value: string(hash), Type: "string"})
}

// ClearFolderPassword 取消目录密码。
func ClearFolderPassword(dir string) error {
	return DeleteSetting(FolderPwdPrefix + normalizeFolder(dir))
}

// LookupFolderPassword 从 path 起逐级向上找「最近的受保护目录」。
//
// 逐级向上是为了让父目录的密码自然地保护整棵子树：
// 只需给 /media/secret 设一次密码，/media/secret/sub 也自动受保护。
// 返回 (受保护目录, bcrypt 哈希, 是否受保护)。
func LookupFolderPassword(p string) (string, string, bool, error) {
	p = normalizeFolder(p)
	for {
		s, err := GetSetting(FolderPwdPrefix + p)
		switch {
		case err == nil && s != nil && s.Value != "":
			return p, s.Value, true, nil
		case err != nil && err != ErrRecordNotFound:
			return "", "", false, err
		}
		if p == "/" {
			return "", "", false, nil
		}
		p = path.Dir(p)
	}
}

// VerifyFolderPassword 校验目录密码。
// secret 与登录一致：客户端传来的静态哈希，服务端再过一次 bcrypt 比对。
func VerifyFolderPassword(hash, secret string) bool {
	if hash == "" || secret == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}
