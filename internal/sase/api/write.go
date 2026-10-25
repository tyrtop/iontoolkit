package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

var writeMethods = map[string]bool{
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodPost:   true,
	http.MethodDelete: true,
}

func (s *Client) Send(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	method = strings.ToUpper(method)
	if !writeMethods[method] {
		return nil, fmt.Errorf("%s is not a write; reads go through GetJSON", method)
	}
	if err := checkPath(path, "write"); err != nil {
		return nil, err
	}
	return s.call(ctx, method+" "+strings.TrimPrefix(path, "/"), method, apiHost()+path, body, true)
}
