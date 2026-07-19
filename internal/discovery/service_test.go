package discovery

import (
	"encoding/json"
	"testing"
)

func TestAnnouncementRoundTrip(t *testing.T) {
	value := Announcement{Version: 1, DeviceID: "id", DeviceName: "PC", TransferPort: 9528}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Announcement
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != value {
		t.Fatalf("decoded=%+v", decoded)
	}
}
