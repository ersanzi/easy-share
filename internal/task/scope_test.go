package task

import (
	"path/filepath"
	"testing"
)

func TestTaskOwnerAndPathTombstoneSurviveRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.json")
	store := NewStore()
	store.EnablePersistence(file)
	value, err := store.Create(Task{OwnerID: "owner", Kind: KindCloudDownload, LocalPath: "private.pdf", FileName: "private.pdf", Status: StatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFor(value.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStore()
	reloaded.EnablePersistence(file)
	hidden, ok := reloaded.Get(value.ID)
	if !ok || hidden.OwnerID != "owner" || !hidden.Hidden || hidden.LocalPath != "private.pdf" {
		t.Fatal("清历史或重启丢失路径归属")
	}
	if len(reloaded.ListFor("owner")) != 0 || len(reloaded.ListFor("other")) != 0 {
		t.Fatal("隐藏路径记录仍作为任务展示")
	}
}
