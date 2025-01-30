package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port        string `yaml:"port" env:"SERVER_PORT"`
		Domain      string `yaml:"domain" env:"SERVER_DOMAIN"`
		RSAKeyFile  string `yaml:"rsa_key_file" env:"RSA_KEY_FILE"`
		TLSKeyFile  string `yaml:"tls_key" env:"TLS_KEY_FILE"`
		TLSCertFile string `yaml:"tls_certificate" env:"TLS_CERT_FILE"`
		CertBundle  string `yaml:"cert_bundle" env:"CERT_BUNDLE"`
	} `yaml:"server"`
	Storage struct {
		RootDirectory string `yaml:"root_directory" env:"STORAGE_ROOT"`
	} `yaml:"storage"`
	Database struct {
		Path string `yaml:"path" env:"DB_PATH"`
	} `yaml:"database"`
	Auth struct {
		Realm   string `yaml:"realm" env:"AUTH_REALM"`
		Service string `yaml:"service" env:"AUTH_SERVICE"`
		Issuer  string `yaml:"issuer" env:"AUTH_ISSUER"`
	} `yaml:"auth"`
}

func Load(configPath string) (*Config, error) {
	cfg := &Config{}

	// LOAD FROM FILE
	if err := loadFromFile(cfg, configPath); err != nil {
		return nil, fmt.Errorf("failed to load config file: %v", err)
	}

	// LOAD FROM ENV VARS
	if err := loadFromEnv(cfg); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %v", err)
	}

	// EXPAND PATHS
	if err := expandConfigPaths(cfg); err != nil {
		return nil, fmt.Errorf("failed to expand paths: %v", err)
	}

	// ENSURE CERT BUNDLE EXISTS
	if err := ensureCertBundle(cfg); err != nil {
		return nil, fmt.Errorf("FAILED TO ENSURE CERT BUNDLE: %v", err)
	}

	fmt.Printf("CONFIG: %v\n", cfg)
	return cfg, nil
}

func loadFromFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse config file: %v", err)
	}

	return nil
}

func loadFromEnv(cfg *Config) error {
	val := reflect.ValueOf(cfg).Elem()
	return loadEnvRecursive(val, "")
}

func loadEnvRecursive(val reflect.Value, prefix string) error {
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		typeField := typ.Field(i)

		// HANDLE NESTED STRUCTS
		if field.Kind() == reflect.Struct {
			newPrefix := prefix
			if envTag := typeField.Tag.Get("yaml"); envTag != "" {
				if newPrefix == "" {
					newPrefix = envTag
				} else {
					newPrefix = prefix + "_" + envTag
				}
			}
			if err := loadEnvRecursive(field, strings.ToUpper(newPrefix)); err != nil {
				return err
			}
			continue
		}

		// GET ENV TAG
		envTag := typeField.Tag.Get("env")
		if envTag == "" {
			continue
		}

		// CHECK IF ENV VAR EXISTS
		if envVal, exists := os.LookupEnv(envTag); exists {
			if err := setField(field, envVal); err != nil {
				return fmt.Errorf("failed to set field %s: %v", typeField.Name, err)
			}
		}
	}

	return nil
}

func setField(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intVal, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(intVal)
	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(boolVal)
	default:
		return fmt.Errorf("unsupported field type: %v", field.Kind())
	}
	return nil
}

func expandConfigPaths(cfg *Config) error {
	// EXPAND SERVER PATHS
	var err error
	cfg.Server.RSAKeyFile, err = expandPath(cfg.Server.RSAKeyFile)
	if err != nil {
		return err
	}
	cfg.Server.TLSKeyFile, err = expandPath(cfg.Server.TLSKeyFile)
	if err != nil {
		return err
	}
	cfg.Server.TLSCertFile, err = expandPath(cfg.Server.TLSCertFile)
	if err != nil {
		return err
	}
	cfg.Server.CertBundle, err = expandPath(cfg.Server.CertBundle)
	if err != nil {
		return err
	}

	// EXPAND STORAGE AND DB PATHS
	cfg.Storage.RootDirectory, err = expandPath(cfg.Storage.RootDirectory)
	if err != nil {
		return err
	}
	cfg.Database.Path, err = expandPath(cfg.Database.Path)
	if err != nil {
		return err
	}

	return nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	// EXPAND ENV VARS
	expanded := os.ExpandEnv(path)

	// HANDLE HOME DIR
	if strings.HasPrefix(expanded, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		expanded = filepath.Join(home, expanded[2:])
	}

	return filepath.Clean(expanded), nil
}

func ensureCertBundle(cfg *Config) error {
	// CREATE CERT DIRECTORIES
	certDir := filepath.Dir(cfg.Server.CertBundle)
	keyDir := filepath.Dir(cfg.Server.RSAKeyFile)

	if err := os.MkdirAll(certDir, 0755); err != nil {
		return fmt.Errorf("failed to create cert directory: %v", err)
	}
	if err := os.MkdirAll(keyDir, 0755); err != nil {
		return fmt.Errorf("failed to create key directory: %v", err)
	}

	// CHECK IF FILES EXIST
	keyExists := false
	certExists := false

	if _, err := os.Stat(cfg.Server.RSAKeyFile); err == nil {
		keyExists = true
	}
	if _, err := os.Stat(cfg.Server.CertBundle); err == nil {
		certExists = true
	}

	// IF BOTH EXIST, WE'RE DONE
	if keyExists && certExists {
		return nil
	}

	// GENERATE NEW RSA KEY
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate RSA key: %v", err)
	}

	// CREATE CERTIFICATE
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour) // 1 YEAR

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Registry Auth"},
			CommonName:   cfg.Server.Domain,
		},
		DNSNames:              []string{cfg.Server.Domain},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// IP(S) FOR LOCALHOST
	if cfg.Server.Domain == "0.0.0.0" || cfg.Server.Domain == "localhost" ||
		strings.HasPrefix(cfg.Server.Domain, "localhost:") {
		template.IPAddresses = append(template.IPAddresses, net.ParseIP("127.0.0.1"))
		template.IPAddresses = append(template.IPAddresses, net.ParseIP("::1"))
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %v", err)
	}

	// SAVE PRIVATE KEY
	keyFile, err := os.OpenFile(cfg.Server.RSAKeyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create key file: %v", err)
	}
	defer keyFile.Close()

	if err := pem.Encode(keyFile, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}); err != nil {
		return fmt.Errorf("failed to write key file: %v", err)
	}

	// SAVE CERTIFICATE
	certFile, err := os.OpenFile(cfg.Server.CertBundle, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create cert file: %v", err)
	}
	defer certFile.Close()

	if err := pem.Encode(certFile, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	}); err != nil {
		return fmt.Errorf("failed to write cert file: %v", err)
	}

	return nil
}
