package verifyproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultListen         = "127.0.0.1:25565"
	defaultReadTimeout    = 5 * time.Second
	defaultWriteTimeout   = 15 * time.Second
	defaultHeaderTimeout  = 5 * time.Second
	defaultRequestTimeout = 4 * time.Second
	defaultMaxBodyBytes   = 64 << 10
	defaultMaxHeaderBytes = 16 << 10
	defaultRetries        = 2
	defaultRetryBackoff   = 100 * time.Millisecond
	maxProxyResponseBytes = 1 << 20
	maxSecretFileBytes    = 4 << 10
)

var publicIDPattern = regexp.MustCompile(`^srv_[A-Za-z0-9_-]{1,120}$`)

type Config struct {
	PublicID       string
	Upstream       string
	Credential     string
	CredentialFile string
	CAFile         string
	Listen         string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	HeaderTimeout  time.Duration
	RequestTimeout time.Duration
	MaxBodyBytes   int
	MaxHeaderBytes int
	Retries        int
	RetryBackoff   time.Duration
}

func LoadConfig(args []string, env map[string]string) (Config, error) {
	cfg := Config{
		Listen:         envOr(env, "SKINNEXUS_VERIFY_PROXY_LISTEN", defaultListen),
		ReadTimeout:    envDuration(env, "SKINNEXUS_VERIFY_PROXY_READ_TIMEOUT", defaultReadTimeout),
		WriteTimeout:   envDuration(env, "SKINNEXUS_VERIFY_PROXY_WRITE_TIMEOUT", defaultWriteTimeout),
		HeaderTimeout:  envDuration(env, "SKINNEXUS_VERIFY_PROXY_HEADER_TIMEOUT", defaultHeaderTimeout),
		RequestTimeout: envDuration(env, "SKINNEXUS_VERIFY_PROXY_REQUEST_TIMEOUT", defaultRequestTimeout),
		MaxBodyBytes:   envInt(env, "SKINNEXUS_VERIFY_PROXY_MAX_BODY_BYTES", defaultMaxBodyBytes),
		MaxHeaderBytes: envInt(env, "SKINNEXUS_VERIFY_PROXY_MAX_HEADER_BYTES", defaultMaxHeaderBytes),
		Retries:        envInt(env, "SKINNEXUS_VERIFY_PROXY_RETRIES", defaultRetries),
		RetryBackoff:   envDuration(env, "SKINNEXUS_VERIFY_PROXY_RETRY_BACKOFF", defaultRetryBackoff),
		CredentialFile: envOr(env, "SKINNEXUS_VERIFY_PROXY_CREDENTIAL_FILE", ""),
		CAFile:         envOr(env, "SKINNEXUS_VERIFY_PROXY_CA_FILE", ""),
	}
	fs := flag.NewFlagSet("verify-proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.PublicID, "public-id", envOr(env, "SKINNEXUS_VERIFY_PROXY_PUBLIC_ID", ""), "public server id")
	fs.StringVar(&cfg.Upstream, "upstream", envOr(env, "SKINNEXUS_VERIFY_PROXY_UPSTREAM", ""), "upstream API root")
	fs.StringVar(&cfg.CredentialFile, "credential-file", cfg.CredentialFile, "restrictive credential file")
	fs.StringVar(&cfg.CAFile, "ca-file", cfg.CAFile, "private upstream CA file")
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "local listen address")
	fs.DurationVar(&cfg.ReadTimeout, "read-timeout", cfg.ReadTimeout, "local read timeout")
	fs.DurationVar(&cfg.WriteTimeout, "write-timeout", cfg.WriteTimeout, "local write timeout")
	fs.DurationVar(&cfg.HeaderTimeout, "header-timeout", cfg.HeaderTimeout, "local header timeout")
	fs.DurationVar(&cfg.RequestTimeout, "request-timeout", cfg.RequestTimeout, "upstream attempt timeout")
	fs.IntVar(&cfg.MaxBodyBytes, "max-body-bytes", cfg.MaxBodyBytes, "local request body cap")
	fs.IntVar(&cfg.MaxHeaderBytes, "max-header-bytes", cfg.MaxHeaderBytes, "local header cap")
	fs.IntVar(&cfg.Retries, "retries", cfg.Retries, "transient retry count")
	fs.DurationVar(&cfg.RetryBackoff, "retry-backoff", cfg.RetryBackoff, "retry delay")
	if err := fs.Parse(args); err != nil {
		return Config{}, errors.New("verify-proxy: invalid command flags")
	}
	if fs.NArg() != 0 {
		return Config{}, errors.New("verify-proxy: positional arguments are not accepted")
	}
	cfg.Credential = envOr(env, "SKINNEXUS_VERIFY_PROXY_CREDENTIAL", "")
	if cfg.Credential != "" && cfg.CredentialFile != "" {
		return Config{}, errors.New("verify-proxy: credential and credential-file are mutually exclusive")
	}
	if cfg.CredentialFile != "" {
		secret, err := readCredentialFile(cfg.CredentialFile)
		if err != nil {
			return Config{}, err
		}
		cfg.Credential = secret
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if !publicIDPattern.MatchString(c.PublicID) {
		return errors.New("verify-proxy: invalid public-id")
	}
	u, err := url.Parse(c.Upstream)
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("verify-proxy: invalid upstream API root")
	}
	if len(c.Credential) < 32 || len(c.Credential) > 512 {
		return errors.New("verify-proxy: credential must contain 32 to 512 bytes")
	}
	for _, ch := range c.Credential {
		if unicode.IsSpace(ch) || unicode.IsControl(ch) {
			return errors.New("verify-proxy: credential contains whitespace or control characters")
		}
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil || host == "" {
		return errors.New("verify-proxy: invalid listen address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("verify-proxy: invalid listen port")
	}
	for name, value := range map[string]time.Duration{
		"read-timeout": c.ReadTimeout, "write-timeout": c.WriteTimeout, "header-timeout": c.HeaderTimeout,
		"request-timeout": c.RequestTimeout, "retry-backoff": c.RetryBackoff,
	} {
		if value <= 0 || value > 2*time.Minute {
			return fmt.Errorf("verify-proxy: invalid %s", name)
		}
	}
	if c.MaxBodyBytes < 1 || c.MaxBodyBytes > maxProxyResponseBytes || c.MaxHeaderBytes < 1024 || c.MaxHeaderBytes > 65536 || c.Retries < 0 || c.Retries > 5 {
		return errors.New("verify-proxy: invalid size or retry limit")
	}
	if c.CAFile != "" {
		if _, err := readCAFile(c.CAFile); err != nil {
			return errors.New("verify-proxy: invalid upstream CA file")
		}
	}
	return nil
}

func readCredentialFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSecretFileBytes {
		return "", errors.New("verify-proxy: credential file must be a bounded regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", errors.New("verify-proxy: credential file must not be accessible by group or others")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("verify-proxy: credential file could not be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSecretFileBytes+1))
	if err != nil || len(data) > maxSecretFileBytes {
		return "", errors.New("verify-proxy: credential file could not be read")
	}
	return strings.TrimSpace(string(data)), nil
}

func readCAFile(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxProxyResponseBytes {
		return nil, errors.New("CA file unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("CA file contains no certificates")
	}
	return pool, nil
}

func NewHTTPClient(cfg Config) (*http.Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
	if cfg.CAFile != "" {
		pool, err := readCAFile(cfg.CAFile)
		if err != nil {
			return nil, errors.New("verify-proxy: invalid upstream CA file")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func envOr(env map[string]string, key, fallback string) string {
	if value := strings.TrimSpace(env[key]); value != "" {
		return value
	}
	return fallback
}

func envDuration(env map[string]string, key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(env[key])
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return -1
	}
	return value
}

func envInt(env map[string]string, key string, fallback int) int {
	raw := strings.TrimSpace(env[key])
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	return value
}
