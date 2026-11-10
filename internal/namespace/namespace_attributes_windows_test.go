//go:build windows

package namespace

import "testing"

// Pure tests only: never call Register/registerOne or write HKCU.
func TestShellFolderCapabilityValues(t *testing.T) {
	bits := []struct {
		name      string
		got, want uint32
	}{
		{"CANCOPY", sfgaoCanCopy, 0x00000001},
		{"CANLINK", sfgaoCanLink, 0x00000004},
		{"STORAGE", sfgaoStorage, 0x00000008},
		{"HASPROPSHEET", sfgaoHasPropSheet, 0x00000040},
		{"STORAGEANCESTOR", sfgaoStorageAncestor, 0x00800000},
		{"DROPTARGET", sfgaoDropTarget, 0x00000100},
		{"FILESYSANCESTOR", sfgaoFileSysAncestor, 0x10000000},
		{"FOLDER", sfgaoFolder, 0x20000000},
		{"FILESYSTEM", sfgaoFileSystem, 0x40000000},
		{"HASSUBFOLDER", sfgaoHasSubFolder, 0x80000000},
	}
	for _, bit := range bits {
		if bit.got != bit.want {
			t.Errorf("%s = %#x, want %#x", bit.name, bit.got, bit.want)
		}
	}
	got := shellFolderAttributes(Entry{writableFilesystem: true})
	if got != 0xF080014D {
		t.Fatalf("writable attributes = %#x, want 0xF080014D", got)
	}
	for _, bit := range bits {
		if got&bit.want == 0 {
			t.Errorf("missing %s", bit.name)
		}
	}
	// Copy/link describe the filesystem delegate; destructive root verbs stay absent.
	if forbidden := got & (sfgaoCanMove | sfgaoCanRename | sfgaoCanDelete | sfgaoReadOnly); forbidden != 0 {
		t.Errorf("unexpected branded-root capabilities: %#x", forbidden)
	}
	if got := shellFolderAttributes(Entry{}); got != 0x60000000 {
		t.Errorf("legacy attributes changed: %#x", got)
	}
}

func TestSpaceEntriesWritableFilesystemCapabilities(t *testing.T) {
	tests := []struct {
		name  string
		mount SpaceMount
		want  uint32
	}{
		{"cfapi personal", SpaceMount{Kind: "personal", TargetPath: `C:\isolated\account`}, 0xF080014D},
		{"readonly personal", SpaceMount{Kind: "personal", TargetPath: `C:\isolated\account`, ReadOnly: true}, 0x60000000},
		{"readonly shared", SpaceMount{Kind: "shared", TargetPath: `C:\isolated\shared`, ReadOnly: true}, 0x60000000},
		{"writable shared not supported", SpaceMount{Kind: "shared", TargetPath: `C:\isolated\shared`}, 0x60000000},
		{"legacy personal", SpaceMount{Kind: "personal", Port: 19082}, 0x60000000},
		{"explicit webdav", SpaceMount{Kind: "personal", TargetPath: WebDAVUNC(19082)}, 0x60000000},
		{"unc", SpaceMount{Kind: "personal", TargetPath: `\\server\share\account`}, 0x60000000},
		{"relative", SpaceMount{Kind: "personal", TargetPath: `account\root`}, 0x60000000},
		{"drive relative", SpaceMount{Kind: "personal", TargetPath: `C:account`}, 0x60000000},
		{"root relative", SpaceMount{Kind: "personal", TargetPath: `\account`}, 0x60000000},
		{"unknown kind", SpaceMount{Kind: "other", TargetPath: `C:\isolated\account`}, 0x60000000},
		{"empty kind", SpaceMount{TargetPath: `C:\isolated\account`}, 0x60000000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := SpaceEntries("", []SpaceMount{tt.mount})
			if len(entries) != 1 {
				t.Fatalf("got %d entries", len(entries))
			}
			if got := shellFolderAttributes(entries[0]); got != tt.want {
				t.Errorf("attributes = %#x, want %#x", got, tt.want)
			}
		})
	}
	for _, entry := range append(DefaultEntries("", 19081, 19080), LANEntry("", 19080), Entry{CLSID: PersonalCLSID(), TargetPath: `C:\arbitrary`}) {
		if got := shellFolderAttributes(entry); got != 0x60000000 {
			t.Errorf("other entry upgraded: %+v attributes=%#x", entry, got)
		}
	}
}
