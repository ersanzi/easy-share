package drive

import (
	"encoding/json"
	"testing"
)

// TestObjectUnmarshalsStringFileId 钉住一个**只在真机暴露**的契约错配。
//
// 背景：RuoYi 的 JacksonConfig 给所有 Long 注册了 BigNumberSerializer——
// 超出 JS 安全整数（2^53-1）时**序列化成字符串**，因为前端 JS 的 Number
// 装不下雪花 ID。于是控制面返回的是：
//
//	{"path":"a.txt","size":11,"fileId":"2110952103974817794"}
//
// 而 Go 侧 Object.FileId 是 int64，encoding/json 遇到字符串会**直接报错**：
//
//	json: cannot unmarshal string into Go struct field Object.fileId of type int64
//
// 后果不是"某个字段为 0"，而是**整个解析失败** → Objects() 返回 error →
// 文件列表、此电脑挂载盘、cfmount、快搜文件路全部拿不到数据。
//
// 为什么单测一直没发现：所有单测都用**自己构造的** drive.Object 或
// httptest 里**手写的** JSON——手写 JSON 时自然写数字，
// 而真实服务端发的是字符串。真实控制面联调才能暴露（2026-10-16 首次跑到）。
func TestObjectUnmarshalsStringFileId(t *testing.T) {
	// 真实服务端的形状（取自 /easyshare/drive/objects 的实际响应）
	raw := []byte(`{"path":"a.txt","size":11,` +
		`"lastModified":"2026-10-16T04:25:08.391Z",` +
		`"fileId":"2110952103974817794","versionId":1}`)

	var obj Object
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("解析真实控制面 JSON 失败：%v\n"+
			"（控制面把雪花 ID 序列化成字符串，Go 侧必须能接受；"+
			"否则整个 Objects() 调用失败，文件列表/挂载盘全挂）", err)
	}
	if obj.FileId != 2110952103974817794 {
		t.Errorf("FileId = %d，期望 2110952103974817794", obj.FileId)
	}
	if obj.VersionId != 1 {
		t.Errorf("VersionId = %d，期望 1", obj.VersionId)
	}
}

// TestObjectStillAcceptsNumericIds 确保兼容旧后端/手写 JSON 的**数字**形式。
//
// 加字符串支持不该把数字形式弄坏——控制面在 ID 未超安全整数时**会发数字**
// （BigNumberSerializer 只在超范围时才转字符串），所以两种形式都必须能吃。
func TestObjectStillAcceptsNumericIds(t *testing.T) {
	raw := []byte(`{"path":"a.txt","size":11,"fileId":123,"versionId":2}`)
	var obj Object
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("数字形式的 fileId 应能解析：%v", err)
	}
	if obj.FileId != 123 || obj.VersionId != 2 {
		t.Errorf("解析结果 = fileId %d / versionId %d，期望 123 / 2", obj.FileId, obj.VersionId)
	}
}
