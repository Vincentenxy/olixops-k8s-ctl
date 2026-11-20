// Package auth implements platform credential verification without issuing user tokens.
package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	d "olixops-k8s-ctl/internal/domain/auth"
)

const personalTokenType = "efficient-pat+jwt"

type JWTConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	PublicKey string `mapstructure:"public-key"`
	Issuer    string `mapstructure:"issuer"`
	Audience  string `mapstructure:"audience"`
	KeyID     string `mapstructure:"key-id"`
}

type PATConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	PublicKey string `mapstructure:"public-key"`
	Issuer    string `mapstructure:"issuer"`
	Audience  string `mapstructure:"audience"`
	KeyID     string `mapstructure:"key-id"`
}

type Config struct {
	Enabled bool      `mapstructure:"enabled"`
	JWT     JWTConfig `mapstructure:"jwt"`
	PAT     PATConfig `mapstructure:"pat"`
}

type JWT struct {
	jwtConfig JWTConfig
	patConfig PATConfig
	jwtKey    *rsa.PublicKey
	patKey    *rsa.PublicKey
}

func NewJWT(cfg Config) (*JWT, error) {
	verifier := &JWT{jwtConfig: cfg.JWT, patConfig: cfg.PAT}
	if cfg.JWT.Enabled {
		key, err := parsePublic(cfg.JWT.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid company JWT public key")
		}
		verifier.jwtKey = key
	}
	if cfg.PAT.Enabled {
		key, err := parsePublic(cfg.PAT.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid platform PAT public key")
		}
		verifier.patKey = key
	}
	return verifier, nil
}

func parsePublic(raw string) (*rsa.PublicKey, error) {
	if !strings.Contains(raw, "-----BEGIN") {
		der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
		if err != nil {
			return nil, err
		}
		raw = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	key, err := jwt.ParseRSAPublicKeyFromPEM([]byte(raw))
	if err != nil {
		return nil, err
	}
	if key.N.BitLen() < 2048 {
		return nil, fmt.Errorf("RSA key too short")
	}
	return key, nil
}

func claim(c jwt.MapClaims, name string) (string, bool) {
	v, exists := c[name]
	if !exists {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (j *JWT) Verify(raw string) (d.Principal, error) {
	return j.verify(raw, tokenHeaderType(raw) == personalTokenType)
}

func (j *JWT) verify(raw string, pat bool) (d.Principal, error) {
	var key *rsa.PublicKey
	var issuer string
	var audience string
	var keyID string
	if pat {
		if !j.patConfig.Enabled {
			return d.Principal{}, d.ErrUnauthenticated
		}
		key = j.patKey
		issuer = j.patConfig.Issuer
		audience = j.patConfig.Audience
		keyID = j.patConfig.KeyID
	} else {
		if !j.jwtConfig.Enabled {
			return d.Principal{}, d.ErrUnauthenticated
		}
		key = j.jwtKey
		issuer = j.jwtConfig.Issuer
		audience = j.jwtConfig.Audience
		keyID = j.jwtConfig.KeyID
	}
	if key == nil {
		return d.Principal{}, d.ErrUnauthenticated
	}
	options := []jwt.ParserOption{jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt()}
	if issuer != "" {
		options = append(options, jwt.WithIssuer(issuer))
	}
	if audience != "" {
		options = append(options, jwt.WithAudience(audience))
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if keyID != "" && t.Header["kid"] != keyID {
			return nil, d.ErrUnauthenticated
		}
		return key, nil
	}, options...)
	if err != nil || !token.Valid || token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
		return d.Principal{}, d.ErrUnauthenticated
	}
	user, ok := claim(claims, "staffuserid")
	name, nameOK := claim(claims, "name")
	staffNo, staffOK := claim(claims, "staffno")
	if !ok || strings.TrimSpace(user) == "" || !nameOK || !staffOK {
		return d.Principal{}, d.ErrUnauthenticated
	}
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil || !expires.After(time.Now()) {
		return d.Principal{}, d.ErrUnauthenticated
	}
	authSource, authSourceOK := claim(claims, "authSource")
	tokenUse, tokenUseOK := claim(claims, "tokenUse")
	tokenType, tokenTypeOK := token.Header["typ"].(string)
	tokenID, tokenIDOK := claim(claims, "jti")
	subject, subjectOK := claim(claims, "sub")
	if pat {
		if !authSourceOK || !tokenUseOK || !tokenTypeOK || !tokenIDOK || !subjectOK ||
			authSource != "personalToken" ||
			tokenUse != "personalAccessToken" ||
			tokenType != personalTokenType ||
			strings.TrimSpace(tokenID) == "" ||
			subject != user {
			return d.Principal{}, d.ErrUnauthenticated
		}
		return d.Principal{UserID: user, StaffNo: staffNo, Name: name, AuthType: "PAT", TokenID: tokenID, ExpiresAt: expires.Time}, nil
	}
	if authSourceOK || tokenUseOK || tokenType == personalTokenType || tokenIDOK {
		return d.Principal{}, d.ErrUnauthenticated
	}
	return d.Principal{UserID: user, StaffNo: staffNo, Name: name, AuthType: "JWT", ExpiresAt: expires.Time}, nil
}

func tokenHeaderType(raw string) string {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var header struct {
		Type string `json:"typ"`
	}
	if json.Unmarshal(data, &header) != nil {
		return ""
	}
	return header.Type
}
