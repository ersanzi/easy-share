package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var configMutex sync.Mutex
var driveLetterPattern = regexp.MustCompile(`^[A-Za-z]:$`)

const (
	defaultAPIHost       = "127.0.0.1"
	defaultAPIPort       = 19079
	defaultDiscoveryPort = 9527
	defaultTransferPort  = 9528
	defaultWebDAVPort    = 19080
)

// CloudConfig holds RustFS (S3-compatible) connection settings.
// An empty Endpoint means the cloud drive is disabled.
type CloudConfig struct {
	Endpoint          string `json:"endpoint"`
	Region            string `json:"region"`
	AccessKeyID       string `json:"accessKeyId"`
	SecretAccessKey   string `json:"secretAccessKey"`
	Bucket            string `json:"bucket"`
	AllowInsecureHTTP bool   `json:"allowInsecureHttp"`
}

func (c CloudConfig) Enabled() bool { return c.Endpoint != "" }

type Config struct {
	DeviceID       string      `json:"deviceId"`
	DeviceName     string      `json:"deviceName"`
	APIHost        string      `json:"apiHost"`
	APIPort        int         `json:"apiPort"`
	APIToken       string      `json:"apiToken"`
	DiscoveryPort  int         `json:"discoveryPort"`
	TransferPort   int         `json:"transferPort"`
	ReceiveDir     string      `json:"receiveDir"`
	WebDAVRoot     string      `json:"webdavRoot"`
	WebDAVPort     int         `json:"webdavPort"`
	WebDAVUsername string      `json:"webdavUsername"`
	WebDAVPassword string      `json:"webdavPassword"`
	DriveLetter    string      `json:"driveLetter"`
	Cloud          CloudConfig `json:"cloud"`
}

func Load(path string) (Config, error) {
	configMutex.Lock()
	defer configMutex.Unlock()
	return loadUnlocked(path)
}

func loadUnlocked(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var value Config
		if err := json.Unmarshal(data, &value); err != nil {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
		if err := value.Validate(); err != nil {
			return Config{}, fmt.Errorf("validate config: %w", err)
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	value, err := defaultConfig()
	if err != nil {
		return Config{}, err
	}
	if err := saveUnlocked(path, value); err != nil {
		return Config{}, err
	}
	return value, nil
}

func Save(path string, value Config) error {
	configMutex.Lock()
	defer configMutex.Unlock()
	return saveUnlocked(path, value)
}

func saveUnlocked(path string, value Config) error {
	if err := value.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(directory, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("protect temporary config: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	removeTemporary = false
	return nil
}

func (value Config) Validate() error {
	ip := net.ParseIP(value.APIHost)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("API host must be a loopback address")
	}
	for name, port := range map[string]int{
		"API": value.APIPort, "discovery": value.DiscoveryPort,
		"transfer": value.TransferPort, "WebDAV": value.WebDAVPort,
	} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s port must be between 1 and 65535", name)
		}
	}
	if value.DeviceID == "" || value.APIToken == "" || value.WebDAVPassword == "" {
		return errors.New("device ID and service secrets must not be empty")
	}
	if value.DeviceName == "" || value.WebDAVUsername == "" {
		return errors.New("device name and WebDAV username must not be empty")
	}
	if value.ReceiveDir == "" || value.WebDAVRoot == "" {
		return errors.New("receive and WebDAV directories must not be empty")
	}
	if !driveLetterPattern.MatchString(value.DriveLetter) {
		return errors.New("drive letter must use the form Z:")
	}
	return nil
}

func defaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	deviceID, err := randomHex(16)
	if err != nil {
		return Config{}, fmt.Errorf("generate device ID: %w", err)
	}
	apiToken, err := randomHex(32)
	if err != nil {
		return Config{}, fmt.Errorf("generate API token: %w", err)
	}
	webDAVPassword, err := randomHex(24)
	if err != nil {
		return Config{}, fmt.Errorf("generate WebDAV password: %w", err)
	}
	deviceName, err := os.Hostname()
	if err != nil || deviceName == "" {
		deviceName = "EasyShare"
	}

	return Config{
		DeviceID:       deviceID,
		DeviceName:     deviceName,
		APIHost:        defaultAPIHost,
		APIPort:        defaultAPIPort,
		APIToken:       apiToken,
		DiscoveryPort:  defaultDiscoveryPort,
		TransferPort:   defaultTransferPort,
		ReceiveDir:     filepath.Join(home, "Downloads", "EasyShare"),
		WebDAVRoot:     filepath.Join(home, "EasyShare"),
		WebDAVPort:     defaultWebDAVPort,
		WebDAVUsername: "EasyShare",
		WebDAVPassword: webDAVPassword,
		DriveLetter:    "Z:",
	}, nil
}

func randomHex(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
