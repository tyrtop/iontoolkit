package endpoint

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Endpoint struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method,omitempty"`
	Host   string `yaml:"host,omitempty"`
	Path   string `yaml:"path"`
}

type file struct {
	Host      string            `yaml:"host,omitempty"`
	Values    map[string]string `yaml:"values,omitempty"`
	Endpoints []Endpoint        `yaml:"endpoints"`
}

type Set struct {
	Name   string
	host   string
	values map[string]string
	byName map[string]Endpoint
	order  []string
}

var methods = map[string]bool{http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, "WS": true}

func Parse(name string, b []byte) (*Set, error) {
	var f file
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return Build(name, f.Host, f.Values, f.Endpoints)
}

func Build(name, host string, values map[string]string, eps []Endpoint) (*Set, error) {
	if err := checkShape(Endpoint{Host: host, Path: "/"}); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	s := &Set{Name: name, host: host, values: map[string]string{}, byName: map[string]Endpoint{}}
	for k, v := range values {
		s.values[k] = v
	}
	for i, e := range eps {
		if e.Name == "" {
			return nil, fmt.Errorf("%s: endpoint %d has no name", name, i+1)
		}
		if _, dup := s.byName[e.Name]; dup {
			return nil, fmt.Errorf("%s: %s is listed twice", name, e.Name)
		}
		if e.Method = strings.ToUpper(e.Method); e.Method == "" {
			e.Method = http.MethodGet
		}
		if !methods[e.Method] {
			return nil, fmt.Errorf("%s: %s: method %s", name, e.Name, e.Method)
		}
		if err := checkShape(e); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", name, e.Name, err)
		}
		s.byName[e.Name] = e
		s.order = append(s.order, e.Name)
	}
	return s, nil
}

func MustParse(name string, b []byte) *Set {
	s, err := Parse(name, b)
	if err != nil {
		panic(err)
	}
	return s
}

func checkShape(e Endpoint) error {
	if e.Host != "" {
		u, err := url.Parse(e.Host)
		if err != nil || (u.Scheme != "https" && u.Scheme != "wss") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("host %q is not https://name or wss://name", e.Host)
		}
	}
	if !strings.HasPrefix(e.Path, "/") {
		return fmt.Errorf("path %q does not start with /", e.Path)
	}
	p, _, _ := strings.Cut(e.Path, "?")
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return fmt.Errorf("path %q walks out of itself", e.Path)
		}
	}
	_, _, err := split(e.Path)
	return err
}

func split(s string) (lits, keys []string, err error) {
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			return append(lits, s), keys, nil
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			return nil, nil, fmt.Errorf("%q has an unclosed {", s)
		}
		lits, keys = append(lits, s[:i]), append(keys, s[i+1:i+j])
		s = s[i+j+1:]
	}
}

func (e Endpoint) SDWAN() error {
	if e.Host != "" {
		return fmt.Errorf("%s names a host of its own; the tenant's controller answers it", e.Name)
	}
	if !strings.HasPrefix(e.Path, "/sdwan/") {
		return fmt.Errorf("%s: %q does not start with /sdwan/", e.Name, e.Path)
	}
	return nil
}

func (e Endpoint) Placeholders() []string {
	_, keys, _ := split(e.Path)
	return keys
}

func (e Endpoint) FillFrom(ctx map[string]any) (string, error) {
	vals := map[string]string{}
	for _, k := range e.Placeholders() {
		vals[k] = valueAt(ctx, k)
	}
	return e.Fill(vals)
}

func valueAt(ctx map[string]any, path string) string {
	var cur any = ctx
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	switch t := cur.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
	}
	return fmt.Sprint(cur)
}

func (s *Set) Match(method, path string) (string, map[string]string, bool) {
	for _, name := range s.order {
		if v, ok := s.byName[name].Match(method, path); ok {
			return name, v, true
		}
	}
	return "", nil, false
}

func (e Endpoint) Match(method, path string) (map[string]string, bool) {
	if method != e.Method {
		return nil, false
	}
	tmpl, _, _ := strings.Cut(e.Path, "?")
	want, got := strings.Split(tmpl, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return nil, false
	}
	v := map[string]string{}
	for i, w := range want {
		if key, ok := strings.CutPrefix(w, "{"); ok && strings.HasSuffix(key, "}") {
			if got[i] == "" {
				return nil, false
			}
			v[strings.TrimSuffix(key, "}")] = got[i]
			continue
		}
		if w != got[i] {
			return nil, false
		}
	}
	return v, true
}

func (s *Set) SDWAN(name string) (Endpoint, error) {
	e, err := s.Get(name)
	if err != nil {
		return e, err
	}
	return e, e.SDWAN()
}

func (s *Set) Get(name string) (Endpoint, error) {
	e, ok := s.byName[name]
	if !ok {
		return Endpoint{}, fmt.Errorf("%s has no endpoint %s", s.Name, name)
	}
	return e, nil
}

func (s *Set) Host() string {
	return s.host
}

func (s *Set) Value(name string) string {
	return s.values[name]
}

func (s *Set) Path(name string, v map[string]string) (string, error) {
	e, err := s.Get(name)
	if err != nil {
		return "", err
	}
	return e.Fill(v)
}

func (s *Set) URL(name string, v map[string]string) (string, error) {
	return s.URLAt(name, "", v)
}

func (s *Set) URLAt(name, host string, v map[string]string) (string, error) {
	e, err := s.Get(name)
	if err != nil {
		return "", err
	}
	p, err := e.Fill(v)
	if err != nil {
		return "", err
	}
	if host == "" {
		host = e.Host
	}
	if host == "" {
		host = s.host
	}
	if host == "" {
		return "", fmt.Errorf("%s: %s has no host", s.Name, name)
	}
	return strings.TrimSuffix(host, "/") + p, nil
}

func (e Endpoint) Fill(v map[string]string) (string, error) {
	path, query, hasQuery := strings.Cut(e.Path, "?")
	p, err := fill(e.Name, path, v, url.PathEscape)
	if err != nil || !hasQuery {
		return p, err
	}
	q, err := fill(e.Name, query, v, url.QueryEscape)
	return p + "?" + q, err
}

func fill(name, s string, v map[string]string, escape func(string) string) (string, error) {
	lits, keys, err := split(s)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	var b strings.Builder
	for i, key := range keys {
		val, ok := v[key]
		if !ok || val == "" {
			return "", fmt.Errorf("%s needs %s", name, key)
		}
		if val == "." || val == ".." || strings.ContainsAny(val, "/?#%\\") {
			return "", fmt.Errorf("%s: %s %q would change the path", name, key, val)
		}
		b.WriteString(lits[i])
		b.WriteString(escape(val))
	}
	b.WriteString(lits[len(keys)])
	return b.String(), nil
}
