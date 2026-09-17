package helpers

import (
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// Claims 令牌载荷：只放用户 ID，其余信息查库，避免令牌里塞过期数据。
type Claims struct {
	UserID uint `json:"user_id"`
	jwt.RegisteredClaims
}

// SignToken 签发 HS256 令牌。
func SignToken(secret string, uid uint, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: uid,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseToken 解析并校验令牌（过期/签名错误都会返回 error）。
func ParseToken(secret, token string) (*Claims, error) {
	claims := &Claims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(secret), nil
	}); err != nil {
		return nil, err
	}
	return claims, nil
}
