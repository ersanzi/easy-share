package drive

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type runnerResult struct {
	output []byte
	err    error
}

type recordingRunner struct {
	calls   [][]string
	results []runnerResult
}

func (runner *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	runner.calls = append(runner.calls, call)
	if len(runner.results) == 0 {
		return nil, nil
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result.output, result.err
}

func TestWebDAVNetworkPath(t *testing.T) {
	tests := map[string]string{
		"http://127.0.0.1:19080":      `\\127.0.0.1@19080\DavWWWRoot`,
		"http://localhost:19080/docs": `\\localhost@19080\DavWWWRoot\docs`,
		"https://localhost/share":     `\\localhost@SSL\DavWWWRoot\share`,
	}
	for rawURL, expected := range tests {
		t.Run(rawURL, func(t *testing.T) {
			actual, err := WebDAVNetworkPath(rawURL)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("network path = %q, want %q", actual, expected)
			}
		})
	}
}

func TestMapperUsesWindowsWebDAVNetworkPath(t *testing.T) {
	runner := &recordingRunner{results: []runnerResult{
		{err: errors.New("not mapped")},
		{},
	}}
	mapper := NewMapperWithRunner(runner)
	if err := mapper.Map(context.Background(), "z:", "http://127.0.0.1:19080", "EasyShare", "secret"); err != nil {
		t.Fatal(err)
	}
	want := []string{"net", "use", "Z:", `\\127.0.0.1@19080\DavWWWRoot`, "secret", "/user:EasyShare", "/persistent:no"}
	if !reflect.DeepEqual(runner.calls[1], want) {
		t.Fatalf("map command = %#v, want %#v", runner.calls[1], want)
	}
}

func TestMapperReusesExistingOwnedWebDAVDrive(t *testing.T) {
	runner := &recordingRunner{results: []runnerResult{{
		output: []byte(`Status OK
Remote name        \\127.0.0.1@19080\DAVWWWROOT`),
	}}}

	err := NewMapperWithRunner(runner).Map(context.Background(), "z:", "http://127.0.0.1:19080", "EasyShare", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want only ownership query", len(runner.calls))
	}
}

func TestMappingOwnershipAcceptsEquivalentSeparatorsAndCase(t *testing.T) {
	output := []byte(`Remote name \\127.0.0.1@19080/DAVWWWROOT`)
	if !mappingOwnedByEasyShare(output, `\\127.0.0.1@19080\DavWWWRoot`, "http://127.0.0.1:19080") {
		t.Fatal("equivalent EasyShare mapping was not recognized")
	}
}

func TestMappingOwnershipRejectsRemoteWithExpectedPrefix(t *testing.T) {
	output := []byte(`Remote name \\127.0.0.1@19080\DavWWWRoot-foreign`)
	if mappingOwnedByEasyShare(output, `\\127.0.0.1@19080\DavWWWRoot`, "http://127.0.0.1:19080") {
		t.Fatal("mapping with only an expected-path prefix was incorrectly recognized")
	}
}

func TestMapperDoesNotReplaceOccupiedDrive(t *testing.T) {
	runner := &recordingRunner{results: []runnerResult{{output: []byte(`Remote name \\server\share`)}}}
	err := NewMapperWithRunner(runner).Map(context.Background(), "Z:", "http://127.0.0.1:19080", "EasyShare", "secret")
	if !errors.Is(err, ErrDriveOccupied) {
		t.Fatalf("error = %v, want ErrDriveOccupied", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want only ownership query", len(runner.calls))
	}
}

func TestMapperOnlyUnmapsOwnedWebDAVDrive(t *testing.T) {
	runner := &recordingRunner{results: []runnerResult{
		{output: []byte(`Remote name \\127.0.0.1@19080\DavWWWRoot`)},
		{},
	}}
	mapper := NewMapperWithRunner(runner)
	if err := mapper.Unmap(context.Background(), "Z:", "http://127.0.0.1:19080"); err != nil {
		t.Fatal(err)
	}
	want := []string{"net", "use", "Z:", "/delete", "/y"}
	if !reflect.DeepEqual(runner.calls[1], want) {
		t.Fatalf("unmap command = %#v, want %#v", runner.calls[1], want)
	}
}

func TestMapperDoesNotUnmapForeignDrive(t *testing.T) {
	runner := &recordingRunner{results: []runnerResult{{output: []byte(`Remote name \\server\share`)}}}
	err := NewMapperWithRunner(runner).Unmap(context.Background(), "Z:", "http://127.0.0.1:19080")
	if err == nil || err.Error() != "drive mapping is not owned by EasyShare" {
		t.Fatalf("error = %v, want ownership error", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d, want only ownership query", len(runner.calls))
	}
}
