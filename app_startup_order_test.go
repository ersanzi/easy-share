package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// 不启动真实桌面或改注册表；锁定清理先于会话恢复，避免异步挂载后入口被删。
func TestStartupCleansNamespaceBeforeRestoringSession(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Startup" || function.Recv == nil {
			continue
		}
		for _, statement := range function.Body.List {
			expression, ok := statement.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := expression.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (method.Sel.Name == "registerNamespace" || method.Sel.Name == "restoreAccountSession") {
				calls = append(calls, method.Sel.Name)
			}
		}
	}
	if len(calls) != 2 || calls[0] != "registerNamespace" || calls[1] != "restoreAccountSession" {
		t.Fatalf("启动入口清理必须先于恢复会话，实际 %v", calls)
	}
}
