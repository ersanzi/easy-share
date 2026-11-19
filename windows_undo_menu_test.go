//go:build windows

package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"

	"easyshare/internal/shellundo"
)

func TestShellUndoCommandRoutesBeforeApplication(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var mainBody *ast.BlockStmt
	for _, decl := range file.Decls {
		if function, ok := decl.(*ast.FuncDecl); ok && function.Name.Name == "main" {
			mainBody = function.Body
		}
	}
	if mainBody == nil || len(mainBody.List) == 0 {
		t.Fatal("main 入口不存在")
	}
	branch, ok := mainBody.List[0].(*ast.IfStmt)
	if !ok {
		t.Fatal("Shell 转发必须先于 NewApp")
	}
	call, ok := branch.Cond.(*ast.CallExpr)
	if !ok {
		t.Fatal("首个分支不是 Shell 命令检查")
	}
	function, ok := call.Fun.(*ast.Ident)
	if !ok || function.Name != "platformShellCommandHandled" {
		t.Fatal("首个分支不能启动客户端")
	}
	if len(branch.Body.List) != 1 {
		t.Fatal("已处理菜单命令应立即返回")
	}
	if _, ok := branch.Body.List[0].(*ast.ReturnStmt); !ok {
		t.Fatal("Shell launcher 未结束")
	}
}

func TestShellUndoCommandOnlyForwardsSelectedPath(t *testing.T) {
	var got []string
	wanted := errors.New("原生删除错误")
	handled, err := handleShellCommand([]string{shellundo.CommandFlag, `C:\EasyShare\cloud\文档.txt`}, func(paths []string) error { got = paths; return wanted })
	if !handled || !errors.Is(err, wanted) || !reflect.DeepEqual(got, []string{`C:\EasyShare\cloud\文档.txt`}) {
		t.Fatalf("转发或错误传播失败: %v/%v/%q", handled, err, got)
	}
}

func TestShellUndoCommandMalformedNeverStartsSecondApp(t *testing.T) {
	for _, args := range [][]string{{shellundo.CommandFlag}, {shellundo.CommandFlag, ""}, {shellundo.CommandFlag, "a", "b"}, {"other", shellundo.CommandFlag, "a"}} {
		called := false
		handled, err := handleShellCommand(args, func([]string) error { called = true; return nil })
		if !handled || err == nil || called {
			t.Fatalf("无效 flag 不能回落 App 或调用转发: %q => %v/%v/%v", args, handled, err, called)
		}
	}
	for _, args := range [][]string{nil, {"--normal"}, {"not-a-flag"}} {
		handled, err := handleShellCommand(args, func([]string) error { t.Fatal("普通启动被转发"); return nil })
		if handled || err != nil {
			t.Fatalf("普通参数不应被 Shell 接管: %q", args)
		}
	}
}
