package config

import (
	"os"
	"path/filepath"

	"github.com/azhai/gobus/environ"
	"github.com/pkg/errors"
)

// APIBaseURL 业务接口前缀，必须与客户端 ApiClient.defaultApiPrefix 一致。
const APIBaseURL = "/api"

// DefaultAdminPassword 首个管理员的默认口令：.env 里没配 ADMIN_PASSWORD 就用它。
const DefaultAdminPassword = "@Mocca/1"

// DefaultEnvFile 默认配置文件，沿用 gobus/environ 的约定：当前工作目录下的 .env。
const DefaultEnvFile = ".env"

// EnvFileKey 指定配置文件路径的环境变量。
// 只有它必须写成全名 —— 得先知道去哪儿读文件，才谈得上读里面的键。
const EnvFileKey = "MOCCA_ENV_FILE"

// EnvKeyPrefix 系统环境变量的前缀。.env 里写短名（ADMIN_PASSWORD），
// 系统环境变量写全名（MOCCA_ADMIN_PASSWORD），二者等价，这就是 environ 的
// CFG_PREFIX 机制。默认给上这个前缀，老部署里已有的 MOCCA_* 变量才不会失效。
const EnvKeyPrefix = "MOCCA_"

// 配置键。.env 里用这些短名。
const (
	keyAddr          = "ADDR"
	keyDataDir       = "DATA_DIR"
	keyJWTSecret     = "JWT_SECRET"
	keyAdminPassword = "ADMIN_PASSWORD"
	keyTmdbKey       = "TMDB_KEY"
	keyTmdbLang      = "TMDB_LANG"
	keyTmdbProxy     = "TMDB_PROXY"
)

// DefaultTmdbLang 刮削缺省语言：中文简介与译名。
const DefaultTmdbLang = "zh-CN"

// Config 运行期配置。命名与取值集中在这里，handler 不再散落读环境。
type Config struct {
	Addr           string // 监听地址
	DataDir        string // 数据目录（封面/缩略图的隐藏目录在它下面）
	DBFile         string // 账号与业务库
	JWTSecret      string // 令牌签名密钥
	TokenExpiresIn int    // 令牌有效期（小时）
	AllowRegister  bool   // 是否开放注册
	AdminPassword  string // 首个管理员的密码（播种 admin 时使用）

	// TMDB 刮削。密钥可空：为空时刮削接口回一句「未配置 TMDB_KEY」而不是报错崩溃。
	// v3 的 api_key 与 v4 的读令牌（JWT）都填这里，客户端按形态自动选鉴权方式。
	TmdbAPIKey string
	TmdbLang   string // 检索/详情的语言，缺省 zh-CN
	// TmdbProxy 出网代理（如 http://127.0.0.1:7890），用于 api/image.tmdb.org 直连不通的环境。
	// 留空则沿用 http.DefaultTransport 的行为 —— 它会读**进程环境**的 HTTPS_PROXY/HTTP_PROXY。
	// 注意 config 只把自己认识的键读进结构、不会写进进程环境，所以「.env 里写 HTTPS_PROXY」
	// 是不生效的；要走代理且在 .env 里配，就写这个 TMDB_PROXY。
	TmdbProxy string

	EnvFile   string // 实际读取的配置文件路径
	EnvLoaded bool   // 该文件确实存在且读取成功
}

// Cfg 全局配置，由 Load 写入。
var Cfg *Config

// Load 载入配置，优先级：内置默认值 < 系统环境变量 < .env 文件。
//
// .env 是权威来源 —— 这是 gobus/environ 的既有语义（先查文件、再查环境变量）。
// 配置文件默认是当前工作目录下的 .env，可用 MOCCA_ENV_FILE 指向别处；
// 文件不存在不算错误（此时只认系统环境变量），但文件读不动（损坏/是目录）会返回
// error：这种绝不能静默降级，否则「配了新口令却没生效、服务还在用默认口令」
// 会一直潜到线上才发现。
//
// 支持的键（.env 短名 / 系统环境变量全名）：
//
//	ADDR           / MOCCA_ADDR            监听地址
//	DATA_DIR       / MOCCA_DATA_DIR        数据目录
//	JWT_SECRET     / MOCCA_JWT_SECRET      令牌签名密钥
//	ADMIN_PASSWORD / MOCCA_ADMIN_PASSWORD  首个管理员口令，缺省 @Mocca/1
//	TMDB_KEY       / MOCCA_TMDB_KEY        TMDB 密钥（可空＝不启用刮削）
//	TMDB_LANG      / MOCCA_TMDB_LANG       刮削语言，缺省 zh-CN
//	TMDB_PROXY     / MOCCA_TMDB_PROXY      刮削出网代理（如 http://127.0.0.1:7890），留空＝直连
func Load() (*Config, error) {
	path := envFilePath()
	env, loaded, err := loadEnv(path)
	if err != nil {
		return nil, err
	}

	Cfg = &Config{
		Addr:           pick(env, keyAddr, ":8000"),
		DataDir:        pick(env, keyDataDir, "data"),
		JWTSecret:      pick(env, keyJWTSecret, "mocca-dev-secret"),
		AdminPassword:  pick(env, keyAdminPassword, DefaultAdminPassword),
		TmdbAPIKey:     pick(env, keyTmdbKey, ""),
		TmdbLang:       pick(env, keyTmdbLang, DefaultTmdbLang),
		TmdbProxy:      pick(env, keyTmdbProxy, ""),
		TokenExpiresIn: 48,   // 暂未开放成配置项
		AllowRegister:  true, // 暂未开放成配置项
		EnvFile:        path,
		EnvLoaded:      loaded,
	}

	if abs, err := filepath.Abs(Cfg.DataDir); err == nil {
		Cfg.DataDir = abs
	}
	Cfg.DBFile = filepath.Join(Cfg.DataDir, "mocca.db")
	return Cfg, nil
}

// pick 取一项配置：.env 里写成 `KEY=`（空值）等同没配，返回 def。
func pick(env *environ.Environ, key, def string) string {
	if v := env.GetStr(key); v != "" {
		return v
	}
	return def
}

// loadEnv 载入 .env，第二个返回值表示该文件是否真的存在且读成功。
// 文件不存在不报错：environ 返回的实例仍会兜到系统环境变量上。
func loadEnv(path string) (*environ.Environ, bool, error) {
	env, err := environ.LoadEnvFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, errors.WithMessagef(err, "读取配置文件 %s", path)
	}

	// 系统环境变量的键名统一带前缀；.env 里写了 CFG_PREFIX 则以它为准。
	// 不设的话 environ 会去找裸名 ADMIN_PASSWORD，
	// 老部署里的 MOCCA_ADMIN_PASSWORD 就被静默忽略了。
	if env.GetStr(environ.PrefixName, "") == "" {
		prefix := EnvKeyPrefix
		env.PrefixInOS = &prefix
	}
	return env, err == nil, nil
}

func envFilePath() string {
	if p := os.Getenv(EnvFileKey); p != "" {
		return p
	}
	return DefaultEnvFile
}
