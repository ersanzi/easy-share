// Package meetingsmcp 把会议听记的只读数据面暴露为 MCP（Model Context Protocol）
// 工具，供员工自己的 AI 客户端（Cherry Studio / Claude Desktop 等）读取其账号
// 可见的听记记录。对标钉钉听记「连接 AI 工具」；刻意只读——不提供任何写入、
// 删除工具，数据可见性与桌面端登录用户一致。
package meetingsmcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"easyshare/internal/knowledge"
)

// SessionLookup 提供当前知识服务会话（桌面端登录态）；未登录返回错误。
type SessionLookup func() (knowledge.Session, error)

type Options struct {
	// Version 随 EasyShare 版本号写入 serverInfo。
	Version string
	// BaseURL 是本机 Core 基址（如 http://127.0.0.1:19080），用于拼音频地址。
	BaseURL string
	Lookup  SessionLookup
}

// listIn / summaryIn 等入参结构即 MCP inputSchema（SDK 由反射推导），
// 字段语义写进工具 Description，不依赖 schema 描述能力。

type listIn struct {
	Limit int `json:"limit,omitempty"`
}

type idIn struct {
	ID string `json:"id"`
}

type askIn struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

// New 构建听记只读 MCP 服务端：list_meetings / get_meeting_summary /
// get_meeting_transcript / get_meeting_keywords / get_meeting_todos /
// get_meeting_audio / ask_meeting 七个工具（前六个对齐钉钉听记 MCP 的只读数据面
// 六件套：列表/摘要/转写原文/关键词/待办/音频地址）。
func New(opts Options) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "easyshare-meetings", Version: opts.Version},
		&mcp.ServerOptions{
			Instructions: "EasyShare 会议听记只读工具集。先用 list_meetings 拿到会议列表" +
				"（返回 id 与一句话摘要），再按需取摘要/逐字稿/关键词/待办/录音地址，或直接提问。" +
				"写周报或追进度时优先组合 list_meetings + get_meeting_todos。" +
				"数据可见范围 = 用户在 EasyShare 桌面端登录的账号。",
		},
	)
	client := func(ctx context.Context) (*knowledge.Client, knowledge.Session, error) {
		session, err := opts.Lookup()
		if err != nil {
			return nil, knowledge.Session{}, err
		}
		return knowledge.NewClient(session.ServerURL), session, nil
	}
	result := func(text string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
	}
	fail := func(format string, args ...any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
			IsError: true,
		}, nil
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_meetings",
		Description: "列出会议听记记录（不含全文）。limit 可选，默认 20；返回每条的 id、标题、时间、时长、发言人数与一句话摘要预览。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[listIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		limit := params.Arguments.Limit
		if limit <= 0 {
			limit = 20
		}
		meetings, err := knowledgeClient.Meetings(ctx, session.Token, limit)
		if err != nil {
			return fail("获取听记列表失败：%v", err)
		}
		if len(meetings) == 0 {
			return result("暂无听记记录"), nil
		}
		var builder strings.Builder
		for _, meeting := range meetings {
			fmt.Fprintf(&builder, "[%s] %s | %s", meeting.ID, meeting.Title, meeting.CreatedAt)
			if meeting.DurationSeconds != nil {
				minutes, seconds := 0, 0
				total := int(*meeting.DurationSeconds)
				minutes, seconds = total/60, total%60
				fmt.Fprintf(&builder, " | 时长 %02d:%02d", minutes, seconds)
			}
			if meeting.SpeakerCount > 0 {
				fmt.Fprintf(&builder, " | %d 位发言人", meeting.SpeakerCount)
			}
			if preview := meetingPreview(&meeting); preview != "" {
				fmt.Fprintf(&builder, " | 摘要：%s", preview)
			}
			builder.WriteString("\n")
		}
		return result(strings.TrimRight(builder.String(), "\n")), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_meeting_summary",
		Description: "获取一条听记的 AI 总结：纪要（按章节）、关键要点与待办清单（含负责人与期限）。参数 id 来自 list_meetings。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[idIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		meeting, err := knowledgeClient.Meeting(ctx, session.Token, params.Arguments.ID)
		if err != nil {
			return fail("获取听记失败：%v", err)
		}
		return result(meetingSummaryText(&meeting)), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_meeting_transcript",
		Description: "获取一条听记的逐字稿全文（说话人名 + 时间戳 + 原文）。长会议会很长，优先用 get_meeting_summary 或 ask_meeting。参数 id 来自 list_meetings。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[idIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		meeting, err := knowledgeClient.Meeting(ctx, session.Token, params.Arguments.ID)
		if err != nil {
			return fail("获取听记失败：%v", err)
		}
		if len(meeting.Segments) == 0 {
			return result("该记录没有转写全文"), nil
		}
		var builder strings.Builder
		for _, segment := range meeting.Segments {
			total := int(segment.Start)
			fmt.Fprintf(&builder, "%s [%02d:%02d] %s\n", speakerName(&meeting, segment.Speaker), total/60, total%60, segment.Text)
		}
		return result(strings.TrimRight(builder.String(), "\n")), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_meeting_keywords",
		Description: "获取一条听记的 AI 关键词（6-10 个短词：项目名/人名/指标/期限，按重要性排序）。适合快速判断这条听记讲什么。参数 id 来自 list_meetings。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[idIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		meeting, err := knowledgeClient.Meeting(ctx, session.Token, params.Arguments.ID)
		if err != nil {
			return fail("获取听记失败：%v", err)
		}
		if len(meeting.Keywords) == 0 {
			return result("该记录暂无关键词（可能尚未生成，可在 EasyShare 听记详情页点「生成关键词」补跑）"), nil
		}
		return result(strings.Join(meeting.Keywords, "、")), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_meeting_todos",
		Description: "获取一条听记的待办清单（每条含负责人、期限与是否完成）。参数 id 来自 list_meetings。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[idIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		meeting, err := knowledgeClient.Meeting(ctx, session.Token, params.Arguments.ID)
		if err != nil {
			return fail("获取听记失败：%v", err)
		}
		if len(meeting.Todos) == 0 {
			return result("该记录没有待办"), nil
		}
		var builder strings.Builder
		for _, todo := range meeting.Todos {
			builder.WriteString(todoText(&todo) + "\n")
		}
		fmt.Fprintf(&builder, "（共 %d 条，未完成 %d 条）", len(meeting.Todos), pendingTodos(meeting.Todos))
		return result(strings.TrimRight(builder.String(), "\n")), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_meeting_audio",
		Description: "获取一条听记的原始录音下载地址（WAV，16kHz 单声道）。用连接本 MCP 的同一个配对令牌作为 Authorization: Bearer <token> 直接 GET 该地址即可取到音频。参数 id 来自 list_meetings。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[idIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		id := strings.TrimSpace(params.Arguments.ID)
		if id == "" {
			return fail("id 不能为空")
		}
		meeting, err := knowledgeClient.Meeting(ctx, session.Token, id)
		if err != nil {
			return fail("获取听记失败：%v", err)
		}
		audioURL := strings.TrimRight(opts.BaseURL, "/") + "/mcp/meetings/" + url.PathEscape(id) + "/audio"
		var builder strings.Builder
		fmt.Fprintf(&builder, "录音地址：%s\n", audioURL)
		fmt.Fprintf(&builder, "标题：%s\n鉴权：Authorization: Bearer <MCP 配对令牌>", meeting.Title)
		if meeting.DurationSeconds != nil {
			total := int(*meeting.DurationSeconds)
			fmt.Fprintf(&builder, "\n时长：%02d:%02d", total/60, total%60)
		}
		return result(builder.String()), nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ask_meeting",
		Description: "对一条听记提问（EasyShare 服务端 LLM 基于该会议的纪要与转写全文作答，通常 5-30 秒）。参数 id 来自 list_meetings，question 为中文问题。",
	}, func(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[askIn]) (*mcp.CallToolResult, error) {
		knowledgeClient, session, err := client(ctx)
		if err != nil {
			return fail("EasyShare 未登录知识服务：%v", err)
		}
		question := strings.TrimSpace(params.Arguments.Question)
		if question == "" {
			return fail("question 不能为空")
		}
		resultData, err := knowledgeClient.MeetingAsk(ctx, session.Token, params.Arguments.ID, question, nil)
		if err != nil {
			return fail("听记问答失败：%v", err)
		}
		return result(resultData.Answer), nil
	})

	return server
}

func meetingPreview(meeting *knowledge.Meeting) string {
	summary := meeting.Summary
	if summary == nil {
		return ""
	}
	text := summary.Minutes
	if text == "" && len(summary.Sections) > 0 {
		text = summary.Sections[0].Text
	}
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) > 80 {
		text = string([]rune(text)[:80]) + "…"
	}
	return text
}

func speakerName(meeting *knowledge.Meeting, speaker string) string {
	if name, ok := meeting.SpeakerNames[speaker]; ok && name != "" {
		return name
	}
	return fmt.Sprintf("说话人 %s", speaker)
}

func meetingSummaryText(meeting *knowledge.Meeting) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "【%s】%s\n", meeting.Title, meeting.CreatedAt)
	summary := meeting.Summary
	if summary == nil {
		builder.WriteString("该记录还没有 AI 总结（可能仍在转写或生成失败）")
		return builder.String()
	}
	if len(summary.Sections) > 0 {
		for _, section := range summary.Sections {
			fmt.Fprintf(&builder, "\n【%s】\n%s\n", section.Title, section.Text)
		}
	} else {
		if summary.Minutes != "" {
			fmt.Fprintf(&builder, "\n【纪要】\n%s\n", summary.Minutes)
		}
		if len(summary.KeyPoints) > 0 {
			builder.WriteString("\n【要点】\n")
			for _, point := range summary.KeyPoints {
				fmt.Fprintf(&builder, "- %s\n", point)
			}
		}
	}
	if len(meeting.Todos) > 0 {
		builder.WriteString("\n【待办】\n")
		for _, todo := range meeting.Todos {
			builder.WriteString(todoText(&todo) + "\n")
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}

// todoText 待办的单行文本（与知识服务 _todo_line 同口径：内容（负责人，期限）+ 完成标记）。
func todoText(todo *knowledge.MeetingTodo) string {
	var parts []string
	if todo.OwnerHint != "" {
		parts = append(parts, todo.OwnerHint)
	}
	if todo.DueHint != "" {
		parts = append(parts, todo.DueHint)
	}
	text := "- [ ] " + todo.Task
	if todo.Done {
		text = "- [x] " + todo.Task
	}
	if len(parts) > 0 {
		text += fmt.Sprintf("（%s）", strings.Join(parts, "，"))
	}
	if todo.Done {
		text += "〔已完成〕"
	}
	return text
}

func pendingTodos(todos []knowledge.MeetingTodo) int {
	count := 0
	for _, todo := range todos {
		if !todo.Done {
			count++
		}
	}
	return count
}
