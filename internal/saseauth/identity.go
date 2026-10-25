package saseauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Identity struct {
	Name           string
	Email          string
	Subject        string
	Scope          string
	TSG            string
	SessionExpires time.Time
}

func (i Identity) SessionLeft() (time.Duration, bool) {
	if i.SessionExpires.IsZero() {
		return 0, false
	}
	return time.Until(i.SessionExpires), true
}

func (i Identity) Display() string {
	switch {
	case i.Name != "" && i.Email != "":
		return i.Name + " <" + i.Email + ">"
	case i.Name != "":
		return i.Name
	case i.Email != "":
		return i.Email
	case i.Subject != "":
		return i.Subject
	}
	return "unknown"
}

func (i Identity) ScopedTo(tsg string) bool {
	if tsg == "" {
		return true
	}
	if i.TSG != "" {
		return i.TSG == tsg
	}
	if i.Scope == "" {
		return true
	}
	for _, s := range strings.Fields(i.Scope) {
		if s == "tsg_id:"+tsg {
			return true
		}
	}
	return false
}

func SessionExpiry(refreshToken string) (time.Time, bool) {
	c := jwtClaims(refreshToken)
	if c == nil {
		return time.Time{}, false
	}
	exp := claimTime(c, "exp")
	return exp, !exp.IsZero()
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}

func claimString(claims map[string]any, key string) string {
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

func claimTime(claims map[string]any, key string) time.Time {
	switch v := claims[key].(type) {
	case float64:
		return time.Unix(int64(v), 0)
	case int64:
		return time.Unix(v, 0)
	}
	return time.Time{}
}

func identityFrom(tok *Token) Identity {
	if tok == nil {
		return Identity{}
	}
	id := Identity{Scope: tok.Scope}

	if c := jwtClaims(tok.IDToken); c != nil {
		id.Name = claimString(c, "name")
		if given, family := claimString(c, "given_name"), claimString(c, "family_name"); given != "" {
			id.Name = strings.TrimSpace(given + " " + family)
		}
		id.Email = claimString(c, "email")
		id.Subject = claimString(c, "sub")
	}

	if c := jwtClaims(tok.RefreshToken); c != nil {
		id.SessionExpires = claimTime(c, "exp")
	}

	if c := jwtClaims(tok.AccessToken); c != nil {
		id.TSG = claimString(c, "tsg_id")
		if id.Subject == "" {
			id.Subject = claimString(c, "sub")
		}
		if id.Scope == "" {
			if raw, ok := c["scope"].([]any); ok {
				var parts []string
				for _, s := range raw {
					if str, ok := s.(string); ok {
						parts = append(parts, str)
					}
				}
				id.Scope = strings.Join(parts, " ")
			}
		}
	}
	return id
}
