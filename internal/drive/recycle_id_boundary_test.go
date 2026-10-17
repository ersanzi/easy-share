package drive_test

// 活栈回归：回收站 ID 必须能**无损往返**到 JS 边界再回来。
//
// 这条来自一次实弹发现：RecycleEntry.FileId 曾经以 JSON number 交给前端，
// 而雪花 ID（19 位）超出 JS 安全整数 2^53-1，会被**静默截断**——
// 实测 2110992378747158530 到前端变成 …800（差 30），传回来查不到任何行。
// 本机 13 行真实 ID 经一次往返**全部改变**（known-issues.md KI-21）。
//
// 为什么必须用活栈数据：单测里 fileId 是白己编的小数字（几百几千），
// **永远撞不到这个问题**——只有真实控制面签发的雪花 ID 才够大。
// 这又是「输入是自己编的」那一族（KI-18 / KI-20）。

import (
	"context"
	"strconv"
	"testing"

	"easyshare/internal/drive"
)

// TestLiveRecycleFileIDSurvivesJSBoundary 验证格式化/解析这一对互逆，
// 且结果确实是那个**超出 JS 安全整数的真实 ID**。
func TestLiveRecycleFileIDSurvivesJSBoundary(t *testing.T) {
	baseURL := liveEnv(t)
	token := login(t, baseURL, "EASYSHARE_LIVE_USER_A", "EASYSHARE_LIVE_PASS_A")
	client := drive.New(baseURL)
	ctx := context.Background()

	entries, err := client.RecycleList(ctx, token, drive.SpacePersonal)
	if err != nil {
		t.Fatalf("读回收站失败: %v", err)
	}
	if len(entries) == 0 {
		t.Skip("回收站为空，无法验证雪花 ID 往返（先跑一次删除类用例）")
	}

	const jsSafeInteger = int64(1)<<53 - 1
	checked := 0
	for _, entry := range entries {
		raw := entry.FileId.Int64()
		if raw <= jsSafeInteger {
			continue // 只有超范围的才能暴露精度问题
		}
		checked++

		view := entry.View()
		// ① 交给前端的必须是字符串形态，且逐字符一致
		if view.FileId != strconv.FormatInt(raw, 10) {
			t.Fatalf("前端形态的 fileId 不是原值的十进制字符串：%q vs %d", view.FileId, raw)
		}
		// ② 解析回来必须完全相同（这就是「无损往返」）
		parsed, err := drive.ParseFileID(view.FileId)
		if err != nil {
			t.Fatalf("解析自身发出的 fileId 失败: %v", err)
		}
		if parsed != raw {
			t.Fatalf("往返后 ID 变了：%d -> %d（前端会因此操作到错误的行或查不到）", raw, parsed)
		}
		t.Logf("✅ 雪花 ID 无损往返：%d", raw)
		break
	}
	if checked == 0 {
		t.Skip("回收站里没有超出 JS 安全整数的 ID，无法验证")
	}
}

// TestParseFileIDRejectsGarbage 守边界：前端传来非法值时要报错，而不是静默当成 0。
//
// 为什么值得钉：ParseInt 失败返回 0，而 0 在控制面语义上是「未登记」——
// 若吞掉错误继续走，就会拿 0 去查库/删东西。宁可报错。
func TestParseFileIDRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-1", "1.5", "  "} {
		if _, err := drive.ParseFileID(bad); err == nil {
			t.Errorf("ParseFileID(%q) 应报错，实得 nil", bad)
		}
	}
	// 合法值必须通过（含超出 JS 安全整数的真实雪花 ID）
	for _, good := range []string{"1", "2110992378747158530"} {
		id, err := drive.ParseFileID(good)
		if err != nil {
			t.Errorf("ParseFileID(%q) 应成功，实得 %v", good, err)
			continue
		}
		if strconv.FormatInt(id, 10) != good {
			t.Errorf("ParseFileID(%q) 往返不一致，实得 %d", good, id)
		}
	}
}

// TestVisibilityDeptRefsRejectSnowflakeCollision 钉住部门 ID 的同一类陷阱。
//
// 部门 ID 也是雪花 ID（实测 sys_dept 里 18 位，如 1761000000000000100）。
// 经 JS number 一次往返，本机 4 个相邻部门 ID **全部**塌成同一个值
// 1761000000000000000（实测 4/4）——「只给研发部看」会静默变成
// 「给某个不存在的部门看」，而**不报任何错**。
//
// 这条不依赖活栈：它检验的是「字符串是否被逐位保留」。
func TestVisibilityDeptRefsRejectSnowflakeCollision(t *testing.T) {
	// 本机真实部门 ID（来自 sys_dept）
	real := []string{
		"1761000000000000100",
		"1761000000000000101",
		"1761000000000000102",
		"1761000000000000103",
	}

	// ① 前提：这些值确实超出 JS 安全整数（否则本用例没有意义）
	const jsSafeInteger = int64(1)<<53 - 1
	for _, s := range real {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("测试数据本身不合法: %s", s)
		}
		if v <= jsSafeInteger {
			t.Fatalf("测试数据 %s 未超出 JS 安全整数，本用例失去意义", s)
		}
	}

	// ② 关键：它们经「JS number 语义」会塌成同一个值——
	// 这是本类缺陷的真实机制，也是为什么必须用字符串。
	collapsed := map[string]bool{}
	for _, s := range real {
		v, _ := strconv.ParseInt(s, 10, 64)
		collapsed[strconv.FormatInt(int64(float64(v)), 10)] = true
	}
	if len(collapsed) == 1 {
		t.Logf("已确认：%d 个不同部门 ID 经 JS number 语义全部塌成 %v "+
			"——「给多个部门看」会静默变成「给同一个部门看」", len(real), collapsed)
	} else {
		t.Logf("（本次运行未复现塌缩，塌成 %d 个值；机制仍是精度丢失）", len(collapsed))
	}

	// ③ 守住：字符串形态必须逐位无损，且互不相同
	seen := map[int64]string{}
	for _, s := range real {
		id, err := drive.ParseFileID(s)
		if err != nil {
			t.Fatalf("合法部门 ID %s 被拒: %v", s, err)
		}
		if strconv.FormatInt(id, 10) != s {
			t.Fatalf("部门 ID 往返失真：%s -> %d", s, id)
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("部门 ID %s 与 %s 解析成同一个值 %d——"+
				"多部门可见性会静默退化成单部门", s, prev, id)
		}
		seen[id] = s
	}
}
