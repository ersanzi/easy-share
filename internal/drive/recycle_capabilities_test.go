package drive

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSupportsSafeRecycleRequiresExplicitCapabilities(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		want bool
	}{
		{"空能力", `{}`, false},
		{"旧目录协议仍为1", `{"persistentDirectories":true,"protocolVersion":1}`, false},
		{"仅快照", `{"recycleSnapshots":true}`, false},
		{"缺少批次", `{"recycleSnapshots":true,"recycleOperations":true}`, false},
		{"缺少操作", `{"recycleSnapshots":true,"recycleBatches":true}`, false},
		{"显式关闭快照", `{"recycleSnapshots":false,"recycleBatches":true,"recycleOperations":true}`, false},
		{"上传校验不能代替回收能力", `{"uploadCommitVerification":true}`, false},
		{"全部回收能力", `{"recycleSnapshots":true,"recycleBatches":true,"recycleOperations":true}`, true},
		{"独立上传校验", `{"protocolVersion":1,"persistentDirectories":true,"recycleSnapshots":true,"recycleBatches":true,"recycleOperations":true,"uploadCommitVerification":true}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/easyshare/drive/capabilities" {
					t.Errorf("不能从对象或回收列表猜测能力：%s", r.URL.Path)
				}
				w.Write([]byte(`{"code":200,"data":` + tt.data + `}`))
			}))
			defer closeFn()
			got, err := client.SupportsSafeRecycle(context.Background(), "tok", SpaceShared)
			if err != nil || got != tt.want {
				t.Fatalf("安全回收能力：got %v, err %v, want %v", got, err, tt.want)
			}
		})
	}
}

func TestRecycleCapabilitiesDoNotChangeDirectoryProtocolV1(t *testing.T) {
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":200,"data":{"persistentDirectories":true,"protocolVersion":1}}`))
	}))
	defer closeFn()
	supported, err := client.SupportsPersistentDirectories(context.Background(), "tok", SpacePersonal)
	if err != nil || !supported {
		t.Fatalf("旧version 1目录声明必须继续支持：supported %v, err %v", supported, err)
	}
}

func TestRecycleCapabilityOptionalFieldsDecodeAndSpaceValidation(t *testing.T) {
	requests := 0
	client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"code":200,"data":{"persistentDirectories":true,"protocolVersion":1,"recycleSnapshots":true,"recycleBatches":true,"recycleOperations":true,"uploadCommitVerification":true}}`))
	}))
	defer closeFn()
	caps, err := client.Capabilities(context.Background(), "tok")
	if err != nil || !caps.RecycleSnapshots || !caps.RecycleBatches || !caps.RecycleOperations || !caps.UploadCommitVerification {
		t.Fatalf("新增bool能力未准确解码：%+v, err %v", caps, err)
	}
	if supported, err := client.SupportsSafeRecycle(context.Background(), "tok", "invalid"); err == nil || supported {
		t.Fatalf("未知空间应在请求前拒绝：supported %v, err %v", supported, err)
	}
	if requests != 1 {
		t.Fatalf("未知空间不应发送请求：%d", requests)
	}
	if supported, err := client.SupportsSafeRecycle(context.Background(), "tok", ""); err != nil || !supported {
		t.Fatalf("空空间应兼容个人空间：supported %v, err %v", supported, err)
	}
}

func TestSafeRecycleCapabilityFailuresDoNotAdvertiseSupport(t *testing.T) {
	for _, body := range []string{
		`{"code":200,"data":[]}`,
		`{"code":200,"data":{"recycleOperations":"true"}}`,
		`{"code":500,"msg":"能力查询失败"}`,
	} {
		t.Run(body, func(t *testing.T) {
			client, closeFn := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(body))
			}))
			defer closeFn()
			supported, err := client.SupportsSafeRecycle(context.Background(), "tok", SpacePersonal)
			if err == nil || supported || !strings.Contains(err.Error(), "读取云盘协议能力失败") {
				t.Fatalf("错误能力不能启用安全回收：supported %v, err %v", supported, err)
			}
		})
	}
}
