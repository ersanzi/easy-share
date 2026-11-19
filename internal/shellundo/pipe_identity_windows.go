//go:build windows

package shellundo

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type userIdentity struct {
	SID       string
	SessionID uint32
}

func currentIdentity() (userIdentity, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return userIdentity{}, fmt.Errorf("读取当前 Windows 用户: %w", err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return userIdentity{}, err
	}
	return userIdentity{SID: user.User.Sid.String(), SessionID: session}, nil
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("读取当前 EasyShare 程序路径: %w", err)
	}
	return normalizeLocalPath(exe)
}

func pipeName(identity userIdentity, root, executable string) string {
	sum := sha256.Sum256([]byte(identity.SID + "\x00" + fmt.Sprint(identity.SessionID) + "\x00" + strings.ToLower(root) + "\x00" + strings.ToLower(executable)))
	return fmt.Sprintf(`\\.\pipe\EasyShare.UndoDelete.%x`, sum[:20])
}

func pipeSecurity(identity userIdentity) (*windows.SecurityAttributes, error) {
	// DACL 仅当前用户可连接；不向 Everyone/Authenticated Users 或网络开放。
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + identity.SID + ")")
	if err != nil {
		return nil, fmt.Errorf("创建同用户管道 ACL: %w", err)
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

func verifyPipePeer(handle windows.Handle, server bool, identity userIdentity, executable string) error {
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(handle, &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(handle, &pid)
	}
	if err != nil {
		return fmt.Errorf("确认管道对端进程: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("打开管道对端进程: %w", err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	if user.User.Sid.String() != identity.SID {
		return fmt.Errorf("拒绝其它用户的 Shell 请求")
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(pid, &session); err != nil {
		return err
	}
	if session != identity.SessionID {
		return fmt.Errorf("拒绝其它登录会话的 Shell 请求")
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return err
	}
	peerExe, err := normalizeLocalPath(windows.UTF16ToString(buffer[:size]))
	if err != nil {
		return err
	}
	if !strings.EqualFold(peerExe, executable) {
		return fmt.Errorf("管道对端不是当前 EasyShare 程序")
	}
	return nil
}
