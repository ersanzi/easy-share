package drive

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

var ErrDriveOccupied = errors.New("drive letter is occupied")
var drivePattern = regexp.MustCompile(`^[A-Za-z]:$`)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Mapper struct{ runner CommandRunner }

func NewMapper() *Mapper                               { return &Mapper{runner: execRunner{}} }
func NewMapperWithRunner(runner CommandRunner) *Mapper { return &Mapper{runner: runner} }

func (mapper *Mapper) Map(ctx context.Context, letter, rawURL, username, password string) error {
	letter = strings.ToUpper(letter)
	if !drivePattern.MatchString(letter) {
		return errors.New("invalid drive letter")
	}
	remote, err := WebDAVNetworkPath(rawURL)
	if err != nil {
		return err
	}
	if output, err := mapper.runner.Run(ctx, "net", "use", letter); err == nil && len(output) > 0 {
		if mappingOwnedByEasyShare(output, remote, rawURL) {
			return nil
		}
		return ErrDriveOccupied
	}
	output, err := mapper.runner.Run(ctx, "net", "use", letter, remote, password, "/user:"+username, "/persistent:no")
	if err != nil {
		return fmt.Errorf("map drive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (mapper *Mapper) Unmap(ctx context.Context, letter, expectedURL string) error {
	letter = strings.ToUpper(letter)
	if !drivePattern.MatchString(letter) {
		return errors.New("invalid drive letter")
	}
	remote, err := WebDAVNetworkPath(expectedURL)
	if err != nil {
		return err
	}
	output, err := mapper.runner.Run(ctx, "net", "use", letter)
	if err != nil {
		return err
	}
	if !mappingOwnedByEasyShare(output, remote, expectedURL) {
		return errors.New("drive mapping is not owned by EasyShare")
	}
	output, err = mapper.runner.Run(ctx, "net", "use", letter, "/delete", "/y")
	if err != nil {
		return fmt.Errorf("unmap drive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// mappingOwnedByEasyShare deliberately relies only on the expected remote
// endpoint. The surrounding `net use` labels are localized by Windows, so they
// cannot be parsed reliably across system languages.
func mappingOwnedByEasyShare(output []byte, expectedRemote, expectedURL string) bool {
	normalizedOutput := normalizeMappingValue(string(output))
	return containsMappingValue(normalizedOutput, normalizeMappingValue(expectedRemote)) ||
		containsMappingValue(normalizedOutput, normalizeMappingValue(expectedURL))
}

func containsMappingValue(output, expected string) bool {
	for offset := 0; offset <= len(output)-len(expected); {
		index := strings.Index(output[offset:], expected)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(expected)
		beforeBoundary := index == 0 || isMappingSeparator(output[index-1])
		afterBoundary := end == len(output) || isMappingSeparator(output[end])
		if beforeBoundary && afterBoundary {
			return true
		}
		offset = index + 1
	}
	return false
}

func isMappingSeparator(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func normalizeMappingValue(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "/", `\`))
}

// WebDAVNetworkPath converts an HTTP WebDAV endpoint to the UNC syntax used by
// Windows' WebClient redirector. net use does not accept an HTTP URL directly.
func WebDAVNetworkPath(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("invalid WebDAV URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("WebDAV URL must use http or https")
	}

	host := strings.Trim(parsed.Hostname(), "[]")
	remote := `\\` + host
	if parsed.Scheme == "https" {
		remote += "@SSL"
	}
	if port := parsed.Port(); port != "" {
		remote += "@" + port
	}
	remote += `\DavWWWRoot`
	if path := strings.Trim(parsed.EscapedPath(), "/"); path != "" {
		decoded, decodeErr := url.PathUnescape(path)
		if decodeErr != nil {
			return "", errors.New("invalid WebDAV URL path")
		}
		remote += `\` + strings.ReplaceAll(decoded, "/", `\`)
	}
	return remote, nil
}
