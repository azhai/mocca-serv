package config

import (
	"os"
	"path/filepath"
	"testing"
)

// noEnv 把可能影响结果的环境变量清空，让用例从「全新部署」开始。
// 各环境变量的实际值来自开发机 shell，不清会串味。
func noEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvFileKey, "ADMIN_PASSWORD", "ADDR", "DATA_DIR", "JWT_SECRET", "CFG_PREFIX",
		EnvKeyPrefix + "ADMIN_PASSWORD", EnvKeyPrefix + "ADDR", EnvKeyPrefix + "DATA_DIR", EnvKeyPrefix + "JWT_SECRET"} {
		t.Setenv(k, "")
	}
}

// inTempDir 切到空目录，使默认的 `.env` 一定不存在。
func inTempDir(t *testing.T) string {
	t.Helper()
	noEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// writeEnv 写一份 .env 到 dir。
func writeEnv(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadDefaultsWithoutEnvFile 没有 .env 时用内置默认值（口令 Match/1）。
func TestLoadDefaultsWithoutEnvFile(t *testing.T) {
	inTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("缺配置文件不该报错: %v", err)
	}
	if cfg.EnvLoaded {
		t.Error("文件不存在时 EnvLoaded 应为 false")
	}
	if cfg.EnvFile != DefaultEnvFile {
		t.Errorf("默认配置文件应为 %q，got %q", DefaultEnvFile, cfg.EnvFile)
	}
	if cfg.AdminPassword != DefaultAdminPassword {
		t.Errorf("默认口令应为 %q，got %q", DefaultAdminPassword, cfg.AdminPassword)
	}
	if cfg.Addr != ":8000" || cfg.JWTSecret != "mocca-dev-secret" {
		t.Errorf("其余默认值异常: %+v", cfg)
	}
	if cfg.TokenExpiresIn != 48 || !cfg.AllowRegister {
		t.Errorf("未开放成配置项的值不该被改动: %+v", cfg)
	}
}

// TestLoadFromEnvFile .env 里的各项生效。
func TestLoadFromEnvFile(t *testing.T) {
	dir := inTempDir(t)
	writeEnv(t, dir, `
# mocca 配置
ADDR=0.0.0.0:9000
DATA_DIR=storage
JWT_SECRET=from-dotenv
ADMIN_PASSWORD=s3cret-from-env-file
`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EnvLoaded {
		t.Error("文件存在时 EnvLoaded 应为 true")
	}
	if cfg.Addr != "0.0.0.0:9000" {
		t.Errorf("ADDR 未生效: %q", cfg.Addr)
	}
	if cfg.JWTSecret != "from-dotenv" {
		t.Errorf("JWT_SECRET 未生效: %q", cfg.JWTSecret)
	}
	if cfg.AdminPassword != "s3cret-from-env-file" {
		t.Errorf("ADMIN_PASSWORD 未生效: %q", cfg.AdminPassword)
	}
	// DATA_DIR 会被转成绝对路径，库文件跟着它走
	if want := filepath.Join(dir, "storage"); cfg.DataDir != want {
		t.Errorf("DATA_DIR 应为 %s，got %s", want, cfg.DataDir)
	}
	if want := filepath.Join(dir, "storage", "mocca.db"); cfg.DBFile != want {
		t.Errorf("库文件应随 DATA_DIR 走，got %s", cfg.DBFile)
	}
}

// TestLoadStripsQuotesAndSkipsComments .env 的行解析：引号去掉、注释与空行跳过。
func TestLoadStripsQuotesAndSkipsComments(t *testing.T) {
	dir := inTempDir(t)
	writeEnv(t, dir, `# 整行注释

ADMIN_PASSWORD="Match/1"
JWT_SECRET='quoted-secret'
# 另一条注释
ADDR=:7000`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != "Match/1" {
		t.Errorf("双引号应被去掉，got %q", cfg.AdminPassword)
	}
	if cfg.JWTSecret != "quoted-secret" {
		t.Errorf("单引号应被去掉，got %q", cfg.JWTSecret)
	}
	if cfg.Addr != ":7000" {
		t.Errorf("ADDR 未生效: %q", cfg.Addr)
	}
}

// TestLoadEmptyValueFallsBack 键写了但值为空，等同没配。
func TestLoadEmptyValueFallsBack(t *testing.T) {
	dir := inTempDir(t)
	writeEnv(t, dir, "ADMIN_PASSWORD=\nADDR=\n")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != DefaultAdminPassword {
		t.Errorf("空值应回退到默认口令 %q，got %q", DefaultAdminPassword, cfg.AdminPassword)
	}
	if cfg.Addr != ":8000" {
		t.Errorf("空 ADDR 应回退到 :8000，got %q", cfg.Addr)
	}
}

// TestLoadSystemEnvWithoutFile .env 用短名，系统环境变量用 MOCCA_ 全名，二者等价 ——
// 老部署里导出的 MOCCA_ADMIN_PASSWORD 必须仍然有效。
func TestLoadSystemEnvWithoutFile(t *testing.T) {
	inTempDir(t)
	t.Setenv("MOCCA_ADMIN_PASSWORD", "from-os-env")
	t.Setenv("MOCCA_ADDR", ":6000")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != "from-os-env" {
		t.Errorf("系统环境变量未生效（前缀映射坏了？），got %q", cfg.AdminPassword)
	}
	if cfg.Addr != ":6000" {
		t.Errorf("MOCCA_ADDR 未生效，got %q", cfg.Addr)
	}
}

// TestEnvFileWinsOverSystemEnv 两者都配时以 .env 为准（gobus/environ 的既有语义）。
func TestEnvFileWinsOverSystemEnv(t *testing.T) {
	dir := inTempDir(t)
	writeEnv(t, dir, "ADMIN_PASSWORD=from-file\n")
	t.Setenv("MOCCA_ADMIN_PASSWORD", "from-os-env")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminPassword != "from-file" {
		t.Errorf(".env 应覆盖系统环境变量，got %q", cfg.AdminPassword)
	}
}

// TestLoadCustomEnvFile MOCCA_ENV_FILE 可指向别处。
func TestLoadCustomEnvFile(t *testing.T) {
	dir := inTempDir(t)
	elsewhere := filepath.Join(t.TempDir(), "mocca.env")
	if err := os.WriteFile(elsewhere, []byte("ADMIN_PASSWORD=custom-path\nDATA_DIR=elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvFileKey, elsewhere)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnvFile != elsewhere {
		t.Errorf("应使用 MOCCA_ENV_FILE 指定的路径，got %s", cfg.EnvFile)
	}
	if cfg.AdminPassword != "custom-path" {
		t.Errorf("口令应为 custom-path，got %q", cfg.AdminPassword)
	}
	if cfg.DataDir != filepath.Join(dir, "elsewhere") {
		t.Errorf("DATA_DIR 应相对工作目录解析，got %s", cfg.DataDir)
	}
}

// TestLoadUnreadableEnvFile 路径读不动（这里是目录）必须报错，不能静默用默认口令。
func TestLoadUnreadableEnvFile(t *testing.T) {
	inTempDir(t)
	dir := t.TempDir()

	if _, err := Load(); err != nil {
		t.Fatalf("默认 .env 不存在时不该报错: %v", err)
	}

	t.Setenv(EnvFileKey, dir) // 指向目录
	if _, err := Load(); err == nil {
		t.Error("配置文件是目录时应报错")
	}
}
