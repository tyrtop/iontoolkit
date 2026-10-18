package saseauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Identity is who a sign-in turned out to be, read from the tokens for display
// and for confirming the tenant scope.
type Identity struct {
	Name    string
	Email   string
	Subject string
	Scope   string
	TSG     string
	// SessionExpires is when the refresh token stops working, which is how long
	// this machine can mint tokens before a browser is needed again. It is read
	// from the token rather than assumed, and is zero when the token does not
	// say.
	SessionExpires time.Time
}

// SessionLeft is how long until a browser is needed again, and whether the
// token said at all.
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

// ScopedTo reports whether the token really carries the tenant that was asked
// for. The authorization server can hand back a narrower scope than requested,
// and a token scoped to the wrong tenant fails later in a way that is hard to
// read, so it is worth checking at sign-in.
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

// SessionExpiry reads when a stored refresh token stops working, so a session's
// remaining life can be shown before any token has been minted this run.
func SessionExpiry(refreshToken string) (time.Time, bool) {
	c := jwtClaims(refreshToken)
	if c == nil {
		return time.Time{}, false
	}
	exp := claimTime(c, "exp")
	return exp, !exp.IsZero()
}

// jwtClaims reads a JWT payload without verifying its signature. These tokens
// arrive over TLS from the issuer and are only used to label the session and to
// check the tenant scope, never to make an access decision, so there is nothing
// here for a signature to protect.
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

// claimTime reads a numeric date claim, which json decodes as a float64.
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
