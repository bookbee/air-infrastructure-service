// Package config loads settings from the environment.
//
// One binary, environment chosen at boot via
// AIR_INFRASTRUCTURE_SERVICE__ENVIRONMENT. Every setting is prefixed with the
// service name and nested groups use a double-underscore delimiter, e.g.
// AIR_INFRASTRUCTURE_SERVICE__REDIS__URL — the same shape every other AIR
// service uses for its own settings.
//
// Precedence is process environment, then the .env file, then the default: an
// exported variable wins over the file, which is what an operator overriding
// one value on the command line expects.
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
)

const envPrefix = "AIR_INFRASTRUCTURE_SERVICE__"

// API is where the HTTP server binds.
type API struct {
	Host string
	Port int
}

// Log controls the structured logger.
type Log struct {
	Level  string
	Render string // console | json
}

// Security holds service authentication settings. ServiceTokens maps a token
// to the single service it identifies. Local only: in prod these come from the
// secret backend, never from .env.
type Security struct {
	ServiceTokens map[string]string
	SecretBackend string // env | file | vault
}

// Redis, Postgres and Mongo are the endpoints the broker hands out. The
// defaults address a host-native run; the .env file overrides them with the
// compose service names.
type Redis struct {
	URL string
}

// Postgres defaults to pgbouncer's port, not Postgres's own: consumers are
// meant to arrive through the pooler.
type Postgres struct {
	Host     string
	Port     int
	DB       string
	User     string
	Password string
}

// Mongo is the Mongo endpoint and database name.
type Mongo struct {
	URI string
	DB  string
}

// Policy points at the RBAC document and sets the fallback rate limit.
type Policy struct {
	ConfigPath           string
	DefaultRatePerMinute int
}

// Settings is the whole configuration.
type Settings struct {
	Environment constants.Environment
	API         API
	Log         Log
	Security    Security
	Redis       Redis
	Postgres    Postgres
	Mongo       Mongo
	Policy      Policy
}

// IsProd reports whether this is a production boot.
func (s Settings) IsProd() bool { return s.Environment == constants.EnvProd }

// Default returns the settings that apply with no environment and no .env file
// at all. Exported so tests can build a Settings without touching the process
// environment, the way the Python tests construct Settings(...) directly.
func Default() Settings {
	return Settings{
		Environment: constants.EnvLocal,
		API:         API{Host: "0.0.0.0", Port: 8080},
		Log:         Log{Level: "INFO", Render: "console"},
		Security: Security{
			ServiceTokens: map[string]string{},
			SecretBackend: "env",
		},
		Redis: Redis{URL: "redis://localhost:6379/0"},
		Postgres: Postgres{
			Host: "localhost",
			Port: 6432, // pgbouncer
			DB:   "air",
			User: "air",
		},
		Mongo: Mongo{URI: "mongodb://localhost:27017", DB: "air"},
		Policy: Policy{
			ConfigPath:           "deploy/policy/policy.local.yaml",
			DefaultRatePerMinute: 120,
		},
	}
}

// Load resolves settings from the process environment and the .env file at
// envPath (absent is not an error — a container is configured entirely through
// the environment). It returns an error rather than falling back on an
// unparseable value: booting with a silently-wrong port or rate limit is worse
// than not booting.
func Load(envPath string) (Settings, error) {
	file, err := readDotenv(envPath)
	if err != nil {
		return Settings{}, err
	}
	src := &source{file: file}
	s := Default()

	s.Environment = constants.Environment(src.str("ENVIRONMENT", string(s.Environment)))
	if !s.Environment.Valid() {
		return Settings{}, fmt.Errorf("%sENVIRONMENT: %q is not one of local, staging, prod",
			envPrefix, s.Environment)
	}

	s.API.Host = src.str("API__HOST", s.API.Host)
	s.API.Port = src.intVal("API__PORT", s.API.Port)
	s.Log.Level = src.str("LOG__LEVEL", s.Log.Level)
	s.Log.Render = src.str("LOG__RENDER", s.Log.Render)
	if s.Log.Render != "console" && s.Log.Render != "json" {
		return Settings{}, fmt.Errorf("%sLOG__RENDER: %q is not one of console, json",
			envPrefix, s.Log.Render)
	}

	s.Security.ServiceTokens = ParseServiceTokens(src.str("SECURITY__SERVICE_TOKENS", ""))
	s.Security.SecretBackend = src.str("SECURITY__SECRET_BACKEND", s.Security.SecretBackend)
	switch s.Security.SecretBackend {
	case "env", "file", "vault":
	default:
		return Settings{}, fmt.Errorf("%sSECURITY__SECRET_BACKEND: %q is not one of env, file, vault",
			envPrefix, s.Security.SecretBackend)
	}

	s.Redis.URL = src.str("REDIS__URL", s.Redis.URL)

	s.Postgres.Host = src.str("POSTGRES__HOST", s.Postgres.Host)
	s.Postgres.Port = src.intVal("POSTGRES__PORT", s.Postgres.Port)
	s.Postgres.DB = src.str("POSTGRES__DB", s.Postgres.DB)
	s.Postgres.User = src.str("POSTGRES__USER", s.Postgres.User)
	s.Postgres.Password = src.str("POSTGRES__PASSWORD", s.Postgres.Password)

	s.Mongo.URI = src.str("MONGO__URI", s.Mongo.URI)
	s.Mongo.DB = src.str("MONGO__DB", s.Mongo.DB)

	s.Policy.ConfigPath = src.str("POLICY__CONFIG_PATH", s.Policy.ConfigPath)
	s.Policy.DefaultRatePerMinute = src.intVal(
		"POLICY__DEFAULT_RATE_PER_MINUTE", s.Policy.DefaultRatePerMinute)

	return s, src.err
}

// ParseServiceTokens reads the comma-separated "token:service" form documented
// in .env.example into a token -> service map.
//
// Blank entries and surrounding whitespace are tolerated, and a pair missing
// either half is skipped rather than failing the boot — the Python validator
// behaves the same way, and one malformed entry in a list of tokens should not
// take the whole control plane down.
func ParseServiceTokens(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		token, service, found := strings.Cut(pair, ":")
		token, service = strings.TrimSpace(token), strings.TrimSpace(service)
		if !found || token == "" || service == "" {
			continue
		}
		out[token] = service
	}
	return out
}

// source resolves one key against the environment, then the .env file, then
// the caller's default. Parse failures accumulate on err so Load can report
// the first one after reading everything.
type source struct {
	file map[string]string
	err  error
}

func (s *source) raw(key string) (string, bool) {
	return lookupEnv(envPrefix+key, s.file)
}

func (s *source) str(key, def string) string {
	if v, ok := s.raw(key); ok {
		return v
	}
	return def
}

func (s *source) intVal(key string, def int) int {
	v, ok := s.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		if s.err == nil {
			s.err = fmt.Errorf("%s%s: %q is not an integer", envPrefix, key, v)
		}
		return def
	}
	return n
}
