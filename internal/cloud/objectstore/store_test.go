package objectstore

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestObjectRefValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		value ObjectRef
		valid bool
	}{
		{name: "valid", value: ObjectRef{Bucket: "bucket", Key: "objects/sha256"}, valid: true},
		{name: "empty bucket", value: ObjectRef{Key: "key"}},
		{name: "blank bucket", value: ObjectRef{Bucket: "  ", Key: "key"}},
		{name: "empty key", value: ObjectRef{Bucket: "bucket"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.value.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestPresignInputValidation(t *testing.T) {
	t.Parallel()
	ref := ObjectRef{Bucket: "bucket", Key: "key"}
	for _, test := range []struct {
		name  string
		value PresignUploadPartInput
		valid bool
	}{
		{name: "valid", value: PresignUploadPartInput{ObjectRef: ref, UploadID: "upload", PartNumber: 1, Expires: time.Minute}, valid: true},
		{name: "empty upload", value: PresignUploadPartInput{ObjectRef: ref, PartNumber: 1, Expires: time.Minute}},
		{name: "part zero", value: PresignUploadPartInput{ObjectRef: ref, UploadID: "upload", PartNumber: 0, Expires: time.Minute}},
		{name: "part too large", value: PresignUploadPartInput{ObjectRef: ref, UploadID: "upload", PartNumber: 10_001, Expires: time.Minute}},
		{name: "zero expiry", value: PresignUploadPartInput{ObjectRef: ref, UploadID: "upload", PartNumber: 1}},
		{name: "expiry too long", value: PresignUploadPartInput{ObjectRef: ref, UploadID: "upload", PartNumber: 1, Expires: MaxPresignTTL + time.Second}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.value.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeCompletedPartsSortsWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	input := []CompletedPart{
		{PartNumber: 3, Token: "three"},
		{PartNumber: 1, Token: "one"},
		{PartNumber: 2, Token: "two"},
	}
	original := append([]CompletedPart(nil), input...)
	got, err := NormalizeCompletedParts(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []CompletedPart{
		{PartNumber: 1, Token: "one"},
		{PartNumber: 2, Token: "two"},
		{PartNumber: 3, Token: "three"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeCompletedParts() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatalf("NormalizeCompletedParts() mutated input: %#v", input)
	}
}

func TestNormalizeCompletedPartsRejectsInvalidParts(t *testing.T) {
	t.Parallel()
	for _, parts := range [][]CompletedPart{
		nil,
		{{PartNumber: 1}},
		{{PartNumber: 0, Token: "token"}},
		{{PartNumber: 1, Token: "one"}, {PartNumber: 1, Token: "again"}},
	} {
		if _, err := NormalizeCompletedParts(parts); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("NormalizeCompletedParts(%#v) error = %v, want ErrInvalidInput", parts, err)
		}
	}
}

func TestProviderErrorSupportsKindAndCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("provider detail")
	err := &ProviderError{Operation: "head object", Kind: ErrNotFound, Err: cause}
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("ProviderError does not unwrap stable kind")
	}
	if !errors.Is(err, cause) {
		t.Fatal("ProviderError does not unwrap provider cause")
	}
}
