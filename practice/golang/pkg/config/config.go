package config

import (
	"bytes"
	"os"
	"time"

	"log/slog"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

type Config struct {
	// Database holds DB-related settings. Must be exported for yaml to populate it.
	Database DatabaseConfig `yaml:"database"`
	// Logging holds slog logger configuration.
	Logging LoggingConfig `yaml:"logging"`
}

type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var durationStr string
	if err := value.Decode(&durationStr); err != nil {
		return err
	}

	// Parse duration string like "300ms", "2s", "1h", etc.
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return err
	}

	*d = Duration(duration)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

type DatabaseConfig struct {
	Dialect  string `yaml:"dialect" validate:"required,oneof=postgres mysql sqlite3" comment:"Database dialect: postgres, mysql, sqlite3"`
	Host     string `yaml:"host" validate:"required"`
	Port     int    `yaml:"port" validate:"required"`
	User     string `yaml:"user" validate:"required"`
	Password string `yaml:"password" validate:"required"`
	DBName   string `yaml:"dbname" validate:"required"`

	MaxOpenConns    int      `yaml:"max_open_conns" validate:"gte=0" comment:"Maximum number of open connections to the database."`
	MaxIdleConns    int      `yaml:"max_idle_conns" validate:"gte=0" comment:"Maximum number of idle connections in the pool."`
	ConnMaxLifetime Duration `yaml:"conn_max_lifetime" validate:"gte=0" comment:"Maximum amount of time a connection may be reused."`
	ConnMaxIdleTime Duration `yaml:"conn_max_idle_time" validate:"gte=0" comment:"Maximum amount of time a connection may be idle."`
}

// LoggingConfig controls application logging via slog.
type LoggingConfig struct {
	// Level: debug | info | warn | error (optional; default info)
	Level string `yaml:"level" validate:"omitempty,oneof=debug info warn error"`
	// Format: json | text (optional; default json)
	Format string `yaml:"format" validate:"omitempty,oneof=json text"`
	// AddSource: include source location in logs
	AddSource bool `yaml:"add_source"`
}

func LoadConfig(configFile string) (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		slog.Error("failed to read config file", "path", configFile, "err", err)
		return nil, err
	}

	// Strict YAML decoding: unknown fields cause an error.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		slog.Error("failed to decode config yaml", "path", configFile, "err", err)
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", "err", err)
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) GetDatabaseConfig() *DatabaseConfig {
	return &c.Database
}

// Validate validates config fields using struct tags.
func (c *Config) Validate() error {
	v := validator.New(validator.WithRequiredStructEnabled())
	return v.Struct(c)
}
