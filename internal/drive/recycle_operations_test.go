package drive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const recycleTestFileID int64 = 2110992378747158530
const recycleTestOperation = "delete-original-op-7"

func recycleTestResult(status string) RecycleOperationResult {
	return RecycleOperationResult{
		BatchId: "batch-from-server-42", OperationId: recycleTestOperation,
		FileId: strconv.FormatInt(recycleTestFileID, 10), Path: "server/docs.txt",
		Directory: true, Status: status, MemberCount: 4,
	}
}

func writeRecycleTestResult(t *testing.T, w http.ResponseWriter, result RecycleOperationResult) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": result}); err != nil {
		t.Errorf("写测试响应失败：%v", err)
	}
}

func newRecycleOperationTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operation-token" || r.Header.Get("clientid") != pcClientID {
			t.Errorf("回收协议请求缺少鉴权：%v", r.Header)
		}
		if r.URL.Path == "/easyshare/drive/capabilities" {
			if r.Method != http.MethodGet {
				t.Errorf("能力请求方法错误：%s", r.Method)
			}
			w.Write([]byte(`{"code":200,"data":{"persistentDirectories":true,"protocolVersion":1,"recycleSnapshots":true,"recycleBatches":true,"recycleOperations":true}}`))
			return
		}
		handler(w, r)
	}))
	t.Cleanup(closeFn)
	return client
}

func TestDeleteOperationSendsStableIdentityVersionAndOperation(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(strconv.FormatBool(recursive), func(t *testing.T) {
			var calls atomic.Int32
			client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodDelete || r.URL.Path != "/easyshare/drive/object" {
					t.Errorf("删除路由错误：%s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("删除必须发送JSON：%v", r.Header)
				}
				var body struct {
					FileID    int64  `json:"fileId"`
					Version   *int64 `json:"expectedVersion"`
					Operation string `json:"operationId"`
					Space     string `json:"space"`
					Path      string `json:"path"`
					Recursive *bool  `json:"recursive"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("解析请求失败：%v", err)
					return
				}
				if body.FileID != recycleTestFileID || body.Version == nil || *body.Version != 0 || body.Operation != recycleTestOperation {
					t.Errorf("稳定身份/零版本/原操作没有准确透传：%+v", body)
				}
				if body.Space != SpacePersonal || body.Path != "request/docs.txt/" || body.Recursive == nil || *body.Recursive != recursive {
					t.Errorf("空间/路径/显式递归没有透传：%+v", body)
				}
				writeRecycleTestResult(t, w, recycleTestResult("recycled"))
			})
			version := int64(0)
			got, err := client.DeleteOperation(context.Background(), "operation-token", "", recycleTestFileID,
				"request/docs.txt/", &version, recycleTestOperation, recursive)
			if err != nil {
				t.Fatal(err)
			}
			want := recycleTestResult("recycled")
			if *got != want {
				t.Fatalf("回调结果必须保持服务端目录/批次/路径/成员数：got %+v, want %+v", got, want)
			}
			if calls.Load() != 1 {
				t.Fatalf("删除不得自动重试：%d", calls.Load())
			}
		})
	}
}

func TestDeleteOperationMayOmitPathButNeverIdentity(t *testing.T) {
	client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求失败：%v", err)
			return
		}
		if _, exists := body["path"]; exists || string(body["fileId"]) != strconv.FormatInt(recycleTestFileID, 10) || string(body["expectedVersion"]) != "9" {
			t.Errorf("省略提示路径后仍须保留身份和版本：%s", body)
		}
		writeRecycleTestResult(t, w, recycleTestResult("recycled"))
	})
	version := int64(9)
	if _, err := client.DeleteOperation(context.Background(), "operation-token", SpaceShared,
		recycleTestFileID, "", &version, recycleTestOperation, true); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteOperationValidatesResponseBeforeConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*RecycleOperationResult)
	}{
		{"错误文件ID", func(r *RecycleOperationResult) { r.FileId = "2110992378747158531" }},
		{"缺文件ID", func(r *RecycleOperationResult) { r.FileId = "" }},
		{"错误原操作", func(r *RecycleOperationResult) { r.OperationId = "other-operation" }},
		{"缺原操作", func(r *RecycleOperationResult) { r.OperationId = "" }},
		{"缺批次", func(r *RecycleOperationResult) { r.BatchId = "" }},
		{"缺路径", func(r *RecycleOperationResult) { r.Path = "" }},
		{"无成员", func(r *RecycleOperationResult) { r.MemberCount = 0 }},
		{"负成员", func(r *RecycleOperationResult) { r.MemberCount = -1 }},
		{"准备中不确认", func(r *RecycleOperationResult) { r.Status = "preparing" }},
		{"已彻底删除不是回收确认", func(r *RecycleOperationResult) { r.Status = "purged" }},
		{"未知状态", func(r *RecycleOperationResult) { r.Status = "ok" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := recycleTestResult("recycled")
			tt.change(&result)
			client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeRecycleTestResult(t, w, result)
			})
			version := int64(3)
			got, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
				recycleTestFileID, "docs", &version, recycleTestOperation, true)
			if err == nil || got != nil {
				t.Fatalf("未确认或身份不匹配必须失败：got %+v, err %v", got, err)
			}
		})
	}
}

func TestDeleteOperationIdempotentRestoredIsExplicit(t *testing.T) {
	client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeRecycleTestResult(t, w, recycleTestResult("restored"))
	})
	version := int64(3)
	got, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
		recycleTestFileID, "docs", &version, recycleTestOperation, true)
	if err != nil || got == nil || got.Status != "restored" {
		t.Fatalf("旧删除已还原的明确结果必须原样返回，不可伪装recycled：%+v, %v", got, err)
	}
}

func TestDeleteOperationRejectsInvalidInputsWithoutRequest(t *testing.T) {
	version, negative := int64(2), int64(-1)
	var calls atomic.Int32
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer closeFn()
	for _, tt := range []struct {
		id      int64
		version *int64
		op      string
	}{
		{0, &version, recycleTestOperation}, {-1, &version, recycleTestOperation},
		{recycleTestFileID, nil, recycleTestOperation}, {recycleTestFileID, &negative, recycleTestOperation},
		{recycleTestFileID, &version, ""}, {recycleTestFileID, &version, "  "},
		{recycleTestFileID, &version, " op "},
	} {
		if result, err := client.DeleteOperation(context.Background(), "tok", SpacePersonal, tt.id,
			"same-path", tt.version, tt.op, true); err == nil || result != nil {
			t.Errorf("无效输入不能退回路径删除：%+v, %v", result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("无效输入不应请求服务端：%d", calls.Load())
	}
}

func TestSafeRecycleOldServersFailBeforeMutation(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		body        string
		unsupported bool
	}{
		{"旧端404", 404, "404 page not found", false},
		{"旧目录v1", 200, `{"code":200,"data":{"persistentDirectories":true,"protocolVersion":1}}`, true},
		{"空能力", 200, `{"code":200,"data":{}}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var mutations atomic.Int32
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/easyshare/drive/capabilities" || r.Method != http.MethodGet {
					mutations.Add(1)
					t.Errorf("不支持时不得执行删除/还原或列表推断：%s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer closeFn()
			version := int64(1)
			_, err := client.DeleteOperation(context.Background(), "tok", SpacePersonal, recycleTestFileID, "docs", &version, recycleTestOperation, true)
			if err == nil || tt.unsupported && !errors.Is(err, ErrSafeRecycleUnsupported) {
				t.Fatalf("旧服务端必须失败且可识别：%v", err)
			}
			if _, err := client.RestoreOperation(context.Background(), "tok", SpacePersonal, recycleTestFileID); err == nil {
				t.Fatal("旧服务端不得假称确认还原")
			}
			if _, err := client.QueryRecycleOperation(context.Background(), "tok", SpacePersonal, recycleTestFileID, recycleTestOperation); err == nil {
				t.Fatal("旧服务端不得假称已查询原操作")
			}
			if mutations.Load() != 0 {
				t.Fatalf("发生不安全回退：%d", mutations.Load())
			}
		})
	}
}

func TestRecycleOperationRejectsLegacyOrMalformedResult(t *testing.T) {
	for _, data := range []string{
		`null`, `{}`, `[]`,
		`{"fileId":2110992378747158530,"operationId":"delete-original-op-7","batchId":"b","path":"docs","directory":true,"status":"recycled","memberCount":4}`,
	} {
		t.Run(data, func(t *testing.T) {
			client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"code":200,"data":` + data + `}`))
			})
			version := int64(1)
			if result, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
				recycleTestFileID, "docs", &version, recycleTestOperation, true); err == nil || result != nil {
				t.Fatalf("旧空返回或有损ID必须拒绝：%+v, %v", result, err)
			}
		})
	}
}

func TestDeleteOperationFailureDoesNotRetryByPath(t *testing.T) {
	var deletes atomic.Int32
	client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		deletes.Add(1)
		w.Write([]byte(`{"code":500,"msg":"删除前文件版本已变化，已保留文件"}`))
	})
	version := int64(3)
	result, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
		recycleTestFileID, "docs", &version, recycleTestOperation, true)
	if err == nil || result != nil || !strings.Contains(err.Error(), "版本已变化") || !strings.Contains(err.Error(), recycleTestOperation) {
		t.Fatalf("版本冲突必须失败、保留原操作上下文：%+v, %v", result, err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("失败不得自动退回path删除：%d", deletes.Load())
	}
}

func TestDeleteOperationExplicitRetryKeepsOriginalIdentityVersionAndOperation(t *testing.T) {
	var bodies []string
	client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(bodies) == 1 {
			w.Write([]byte(`{"code":500,"msg":"结果暂未确认，请查询原操作"}`))
			return
		}
		writeRecycleTestResult(t, w, recycleTestResult("recycled"))
	})
	version := int64(3)
	if _, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
		recycleTestFileID, "docs", &version, recycleTestOperation, true); err == nil {
		t.Fatal("第一次未确认应失败")
	}
	if _, err := client.DeleteOperation(context.Background(), "operation-token", SpacePersonal,
		recycleTestFileID, "docs", &version, recycleTestOperation, true); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatalf("调用方显式重试必须保持原身份/版本/op：%v", bodies)
	}
}

func TestQueryRecycleOperationChecksOriginalOperationAndPreservesStatus(t *testing.T) {
	for _, status := range []string{"preparing", "recycled", "restoring", "restored", "purging", "purged"} {
		t.Run(status, func(t *testing.T) {
			op := "原删除 op?&=7"
			result := recycleTestResult(status)
			result.OperationId = op
			client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/easyshare/drive/recycle/operation" {
					t.Errorf("查询只能读取原op，不能重删：%s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("space") != SpaceShared || r.URL.Query().Get("operationId") != op {
					t.Errorf("查询space和原op必须无损转义：%s", r.URL.RawQuery)
				}
				writeRecycleTestResult(t, w, result)
			})
			got, err := client.QueryRecycleOperation(context.Background(), "operation-token", SpaceShared, recycleTestFileID, op)
			if err != nil || got == nil || *got != result {
				t.Fatalf("查询结果目录/路径/状态等必须原样返回：%+v, %v", got, err)
			}
		})
	}
}

func TestQueryRecycleOperationRejectsOtherIdentityAndUnknownState(t *testing.T) {
	for _, change := range []func(*RecycleOperationResult){
		func(r *RecycleOperationResult) { r.FileId = "1" },
		func(r *RecycleOperationResult) { r.OperationId = "other" },
		func(r *RecycleOperationResult) { r.BatchId = "" },
		func(r *RecycleOperationResult) { r.Status = "success" },
	} {
		result := recycleTestResult("recycled")
		change(&result)
		client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) { writeRecycleTestResult(t, w, result) })
		if got, err := client.QueryRecycleOperation(context.Background(), "operation-token", SpacePersonal,
			recycleTestFileID, recycleTestOperation); err == nil || got != nil {
			t.Fatalf("查询不得采纳其他身份/op或未知状态：%+v, %v", got, err)
		}
	}
}

func TestRestoreOperationPostsRootIdentityAndReturnsWholeBatch(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacy), func(t *testing.T) {
			result := recycleTestResult("restored")
			if legacy {
				result.BatchId, result.OperationId, result.Directory, result.MemberCount = "", "", false, 1
			}
			client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/easyshare/drive/recycle/restore" {
					t.Errorf("还原路由错误：%s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("解析还原请求失败：%v", err)
					return
				}
				if len(body) != 2 || string(body["fileId"]) != strconv.FormatInt(recycleTestFileID, 10) || string(body["space"]) != `"shared"` {
					t.Errorf("还原仅按准确fileID/space：%s", body)
				}
				writeRecycleTestResult(t, w, result)
			})
			got, err := client.RestoreOperation(context.Background(), "operation-token", SpaceShared, recycleTestFileID)
			if err != nil || got == nil || *got != result {
				t.Fatalf("还原返回真实完整批次，旧行允许缺批次信息：%+v, %v", got, err)
			}
		})
	}
}

func TestRestoreOperationRequiresExplicitMatchingRestoredResult(t *testing.T) {
	for _, change := range []func(*RecycleOperationResult){
		func(r *RecycleOperationResult) { r.FileId = "1" },
		func(r *RecycleOperationResult) { r.Status = "recycled" },
		func(r *RecycleOperationResult) { r.Status = "restoring" },
		func(r *RecycleOperationResult) { r.Path = "" },
		func(r *RecycleOperationResult) { r.MemberCount = 0 },
	} {
		result := recycleTestResult("restored")
		change(&result)
		client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) { writeRecycleTestResult(t, w, result) })
		if got, err := client.RestoreOperation(context.Background(), "operation-token", SpacePersonal, recycleTestFileID); err == nil || got != nil {
			t.Fatalf("未确认或不匹配还原结果必须失败：%+v, %v", got, err)
		}
	}
	client := newRecycleOperationTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":500,"msg":"原位置已有同名文件，请先处理现有文件后再还原"}`))
	})
	if _, err := client.RestoreOperation(context.Background(), "operation-token", SpacePersonal, recycleTestFileID); err == nil || !strings.Contains(err.Error(), "原位置已有同名文件") {
		t.Fatalf("还原冲突必须原样透出：%v", err)
	}
}

func TestRecycleListViewPreservesDirectoryBatchStateAndStringID(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":[{"fileId":"2110992378747158530","path":"docs.txt","originPath":"docs.txt","size":0,"versionId":3,"directory":true,"batchId":"server-batch-7","snapshotState":"ready"},{"fileId":7,"path":"legacy.txt","originPath":"legacy.txt","size":12,"versionId":1}]}`))
	}))
	defer closeFn()
	entries, err := client.RecycleList(context.Background(), "tok", SpacePersonal)
	if err != nil || len(entries) != 2 {
		t.Fatalf("兼容新根目录行和旧文件行：%+v, %v", entries, err)
	}
	view := entries[0].View()
	if !view.Directory || view.BatchId != "server-batch-7" || view.SnapshotState != "ready" || view.FileId != strconv.FormatInt(recycleTestFileID, 10) {
		t.Fatalf("JS边界不能丢目录/批次/状态/雪花ID：%+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["fileId"].(string); !ok {
		t.Fatalf("JS fileId必须string：%s", raw)
	}
	legacy := entries[1].View()
	if legacy.Directory || legacy.BatchId != "" || legacy.SnapshotState != "" || legacy.FileId != "7" {
		t.Fatalf("缺新增字段的旧回收站行应继续兼容：%+v", legacy)
	}
}

func TestLegacyRecycleAPIsIgnoreNewResultWithoutChangingRequests(t *testing.T) {
	for _, op := range []string{"delete", "restore", "purge"} {
		t.Run(op, func(t *testing.T) {
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/easyshare/drive/capabilities" {
					t.Error("旧API不得新增能力闸门改变兼容性")
				}
				writeRecycleTestResult(t, w, recycleTestResult("restored"))
			}))
			defer closeFn()
			var err error
			switch op {
			case "delete":
				err = client.DeleteByFileID(context.Background(), "tok", SpacePersonal, recycleTestFileID, "docs")
			case "restore":
				err = client.RecycleRestore(context.Background(), "tok", SpacePersonal, recycleTestFileID)
			case "purge":
				err = client.RecyclePurge(context.Background(), "tok", SpacePersonal, recycleTestFileID)
			}
			if err != nil {
				t.Fatalf("旧error-only API应继续忽略新增结果：%v", err)
			}
		})
	}
}
