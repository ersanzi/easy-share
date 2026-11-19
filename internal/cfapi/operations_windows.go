//go:build windows

package cfapi

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

// OperationRequest 是在回调期间复制的操作身份；不保留内核临时指针。
// FileID 是本机NTFS身份，CloudIdentity才是服务器文件身份。
type OperationRequest struct {
	Kind          CallbackType
	Path          string
	TargetPath    string
	SourcePath    string
	CloudIdentity []byte
	FileID        int64
	FileSize      int64
	Flags         uint32
	ParameterSize uint32
	ProcessName   string
	ProcessID     uint32
	OccurredAt    time.Time
	// SupplyData仅在本次前置回调中有效，用操作TransferKey直接回填；不可保存到磁盘。
	SupplyData func([]byte, int64) error `json:"-"`
}

// OperationCallbacks 仅在显式接入写路径时注册；旧只读挂载不改变行为。
// Before返回错误会通过ACK拒绝本机操作；Completed不在内核等待路径中执行。
type OperationCallbacks struct {
	Before    func(context.Context, OperationRequest) error
	Completed func(OperationRequest)
	// Result只报告真实ACK返回，不把内核拒绝的操作当完成。
	Result func(OperationRequest, error)
}

// SDK union从偏移8开始；AckDelete和AckRename为Flags+NTSTATUS，ParamSize=16。
type operationAckParameters struct {
	ParamSize        uint32
	_                uint32
	Flags            uint32
	CompletionStatus int32
}

type operationCallbackParameters struct {
	ParamSize uint32
	_         uint32
	Flags     uint32
	_         uint32
	Path      *uint16
}

const (
	operationTypeAckDelete = 6
	operationTypeAckRename = 7
	OperationSourceInScope = 2
	OperationTargetInScope = 4
	OperationIsDirectory   = 1
	// 官方SDK: NOTIFY_DELETE也用于撤销删除，此位区分删除与原生undelete。
	OperationIsUndelete = 2
)

func operationRequest(info *CallbackInfo, params *operationCallbackParameters, kind CallbackType) OperationRequest {
	name, pid := processOf(info)
	request := OperationRequest{
		Kind: kind, Path: utf16ToString(info.NormalizedPath), FileID: info.FileID, OccurredAt: time.Now(),
		FileSize: info.FileSize, ParameterSize: params.ParamSize,
		ProcessName: name, ProcessID: pid,
		CloudIdentity: append([]byte(nil), unsafeSlice(info.FileIdentity, uintptr(info.FileIdentityLength))...),
	}
	if params.ParamSize >= 12 {
		request.Flags = params.Flags
	}
	if params.ParamSize >= 24 {
		if kind == CallbackTypeNotifyRename {
			request.TargetPath = utf16ToString(params.Path)
		} else if kind == CallbackTypeNotifyRenameCompletion {
			request.SourcePath = utf16ToString(params.Path)
		}
	}
	return request
}

func (s *Session) operationCallback(infoPtr, paramsPtr uintptr, kind CallbackType, before bool) (result uintptr) {
	if infoPtr == 0 || paramsPtr == 0 {
		return 0
	}
	info := *asCallbackInfo(infoPtr)
	params := (*operationCallbackParameters)(*(*unsafe.Pointer)(unsafe.Pointer(&paramsPtr)))
	acknowledged := false
	defer func() {
		if failure := recover(); failure != nil && before && !acknowledged {
			_ = acknowledgeOperation(info, kind, fmt.Errorf("cfapi: 操作回调失败: %v", failure))
		}
	}()
	request := operationRequest(&info, params, kind)
	if before {
		request.SupplyData = func(data []byte, offset int64) error {
			return s.executeTransferChecked(&info, FetchResult{Data: data, Offset: offset})
		}
	}
	if !before {
		if s.operations != nil && s.operations.Completed != nil {
			s.operations.Completed(request)
		}
		return 0
	}
	var err error
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if !active {
		err = fmt.Errorf("cfapi: 操作所属会话已断开")
	} else if s.operations == nil || s.operations.Before == nil {
		err = fmt.Errorf("cfapi: 未提供写操作处理器")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err = s.operations.Before(ctx, request)
		cancel()
	}
	acknowledged = true
	ackErr := acknowledgeOperation(info, kind, err)
	if s.operations != nil && s.operations.Result != nil {
		s.operations.Result(request, errors.Join(err, ackErr))
	}
	return 0
}

func acknowledgeOperation(info CallbackInfo, kind CallbackType, failure error) error {
	operationType := uint32(operationTypeAckDelete)
	if kind == CallbackTypeNotifyRename {
		operationType = operationTypeAckRename
	}
	op := OperationInfo{StructSize: uint32(unsafe.Sizeof(OperationInfo{})), Type: operationType,
		ConnectionKey: info.ConnectionKey, TransferKey: info.TransferKey, RequestKey: info.RequestKey}
	params := operationAckParameters{ParamSize: uint32(unsafe.Sizeof(operationAckParameters{}))}
	if failure != nil {
		params.CompletionStatus = int32(statusCloudFileUnsuccessful)
	}
	call, err := proc("CfExecute")
	if err != nil {
		return err
	}
	hr, _, _ := call.Call(uintptr(unsafe.Pointer(&op)), uintptr(unsafe.Pointer(&params)))
	return checkHResult(hr, "CfExecute ACK operation")
}

func (s *Session) onNotifyDelete(info, params uintptr) uintptr {
	return s.operationCallback(info, params, CallbackTypeNotifyDelete, true)
}
func (s *Session) onNotifyDeleteCompletion(info, params uintptr) uintptr {
	return s.operationCallback(info, params, CallbackTypeNotifyDeleteCompletion, false)
}
func (s *Session) onNotifyRename(info, params uintptr) uintptr {
	return s.operationCallback(info, params, CallbackTypeNotifyRename, true)
}
func (s *Session) onNotifyRenameCompletion(info, params uintptr) uintptr {
	return s.operationCallback(info, params, CallbackTypeNotifyRenameCompletion, false)
}

// RelativeOperationPath将卷相对或绝对通知路径限制到当前根；不按basename猜归属。
func RelativeOperationPath(root, path string) (string, bool) {
	root = filepath.Clean(root)
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		path = filepath.VolumeName(root) + path
	}
	if !filepath.IsAbs(path) {
		return "", false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	rel = filepath.ToSlash(rel)
	if err != nil || !validProtectedEntry(rel) {
		return "", false
	}
	return rel, true
}
