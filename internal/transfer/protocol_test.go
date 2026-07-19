package transfer

import (
	"bytes"
	"testing"
)

func TestMetadataRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	want := Metadata{TaskID: "id", FileName: "a.txt", FileSize: 10}
	if err := writeMessage(&buffer, want); err != nil {
		t.Fatal(err)
	}
	var got Metadata
	if err := readMessage(&buffer, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%+v", got)
	}
}
