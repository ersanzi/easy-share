package cfmount

// WritebackPhase 描述写回过程，不等同于本机是否完整持有正文。
// synced 只在远端全文和版本确认后产生；Shell 仍需核对本机身份及占位状态。
type WritebackPhase string

const (
	WritebackUnknown   WritebackPhase = "unknown"
	WritebackPending   WritebackPhase = "pending"
	WritebackUploading WritebackPhase = "uploading"
	WritebackVerifying WritebackPhase = "verifying"
	WritebackSynced    WritebackPhase = "synced"
	WritebackFailed    WritebackPhase = "failed"
	WritebackConflict  WritebackPhase = "conflict"
)

// WritebackStatus.Path 是同步根内的相对路径；不携带账号凭据或预签名 URL。
// 回调在引擎互斥区内执行，消费者只能投递快照，不得重入引擎或操作 WebView。
type WritebackStatus struct {
	Path             string
	Phase            WritebackPhase
	Total, Completed int64
	Message          string
}

// SyncBaseline 只投影已提交的身份与待上传意图，不复制快照路径或凭据。
// LocalVerified 需要本轮全文快照匹配，不凭文件大小和时间猜同步成功。
type SyncBaseline struct {
	Path                  string
	FileID, Version, Size int64
	Directory, Pending    bool
	LocalVerified         bool
}

func (e *WritebackEngine) publishStatus(path string, phase WritebackPhase, total, completed int64, message string) {
	if fn := e.cfg.OnStatus; fn != nil {
		fn(WritebackStatus{Path: path, Phase: phase, Total: total, Completed: completed, Message: message})
	}
}

// SyncBaselines 返回只读副本，供调度器在 Reconcile 结束后结合 CFAPI 元数据投影状态。
func (e *WritebackEngine) SyncBaselines() map[string]SyncBaseline {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]SyncBaseline, len(e.state.Records))
	for p, r := range e.state.Records {
		_, proof := e.cacheProof[p]
		out[p] = SyncBaseline{Path: p, FileID: r.FileID, Version: r.Version, Size: r.Size, Directory: r.Directory, Pending: r.Pending != nil, LocalVerified: proof && r.Pending == nil}
	}
	return out
}
