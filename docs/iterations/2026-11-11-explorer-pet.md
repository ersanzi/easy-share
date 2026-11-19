# EasyShare Explorer 身份、状态与桌面宠物

## 用户问题与目标

原服务器个人云盘已有条件写回/透明缓存，但缺少与“此电脑”同级的动态入口、默认详细视图与真实状态图标、取代托盘富悬停窗的常驻桌面宠物。

本轮完成圆角矩形雏形，不引入动物动画或新框架；服务器主存与上传/版本保护不变。

## 实现

- `internal/account/display.go` · `DisplayName`：trim昵称优先，空昵称用账号名，稳定空间仍绑定UserID。
- `app_account_refresh.go` · `commitAccountProfile`：资料世代及会话对象守卫，同账号更新不替换挂载授权会话；旧结果不能复活注销状态。
- `cfmount_label_windows.go` · `refreshNativeSpaceLabel`：只重标原根，不重建CFAPI/NTFS/pending。
- `internal/namespace/namespace_windows.go` · `Register`：MyComputer磁贴和Desktop同级导航共用同CLSID及原根，对称清理。
- `internal/shellroot/register_windows.go` · `Register`：WinRT完整提供方展示登记；普通退出只disconnect，可见品牌入口撤销，禁止破坏性注销。
- `internal/shellroot/navigation_windows.go` · `SuppressGeneratedNavigation`：只撤自动生成的重复节点，保留提供方登记。
- `internal/cfmount/writeback_status.go` · `WritebackStatus`：等待/上传/远端全文确认/成功/冲突/失败活动，字节100%不能提前成功。
- `internal/cfapi/sync_status_windows.go` · `QueryPlaceholderStatus`：只读权威占位元数据和实际数据区间，不召回正文；普通未知项不批量刷绿。
- `internal/shellstatus/property_windows.go` · `Apply`：独立COM线程写真实volatile传输活动/进度并通知Shell。
- `internal/explorerview/watcher_windows.go` · `Start`：本云盘默认详细列表、同目录两入口布局恢复及独立首开窄高；用户其他目录不改。
- `internal/explorerview/migration_windows.go` · `migrateDefaultStatusColumn`：只迁移本程序早期五列默认文字状态列，保留列宽/自定义布局。
- `desktop_pet_windows.go` · `newDesktopPet`：常驻原生圆角96×64DIP宠物，位置记忆，悬停展开面板，双击固定的是面板而非位置。
- `tray_hover_windows.go` · `handleDroppedObjects`：保留WebView附加File对象取得真实路径和原服务器上传链路，后台只投递快照，面板收起不取消任务。

## 根因与兼容

1. Explorer导航期间可暂时没有当前browser/view；非负HRESULT不能代替接口存在。返回未就绪并在后续采样重试，不对空vtable调用，不吞panic。
2. 原生图标列是 `System.StorageProviderUIStatus`，不是“可用性状态”文字列；公开只读合成属性不能强写位图。
3. 传输/失败活动必须结合真实尚未提交的修改状态；仅云端且已同步的文件不能伪装上传中的脏文件。失败仍保留修改、不标绿。
4. 本系统Cloud文件volatile进度存储接受CloudMirror UI8 `[completed,total]`；按schema改UI4的A/B在真实云根返回E_INVALIDARG，已撤销试验。终态合法零向量用于去除活动残留。
5. Windows Search禁用导致Commit返回ERROR_SERVICE_DISABLED。经另行批准受控改为手动运行；本机原始默认规则恢复并用明确User排除确保桌面/文档/AppData/全盘和临时fixture均不在有效索引范围内，未添加业务内容索引。
6. Windows原子状态/续传保存可被扫描句柄短暂阻止；只有限重试同一原子操作，永久拒绝继续失败，不删目标、不无条件覆盖。
7. 当前服务器未部署最新权限/头像/SSO拓扑，本轮保持既有缺接口的兼容提示；不声称这些业务已上线。

## 实际验证

- 完整Go回归通过；相关go vet与差异格式通过，前端83用例/13文件、vue-tsc/Vite、Core及Wails候选构建通过。全局剪贴板旧unsafe.Pointer告警未改。
- `desktop_pet_gui_windows_test.go` · `TestDesktopPetIsolatedGUI`：真实鼠标悬停/跨面板/离开收起/双击固定/固定后拖动/解除/正常销毁通过。
- `cloud_state_gui_windows_test.go` · `TestCloudStateExplorerIsolatedGUI`：真实CFAPI占位、绿勾/云朵/未提交同步箭头与行内进度，两入口实际点击及同级层级通过；失败文件不刷绿。失败标志回读通过，独立红色错误图标在Server2022未单独保证。
- `account_binding_live_windows_test.go` · `TestLiveAccountNicknameBinding`：两专用服务器账号A→B→A、改昵称前后根/会话/NTFS身份保持、旧回调拒绝、两处Explorer真实点击通过。专用账号已核实后删除，原管理员会话恢复。
- `cfmount_cache_gui_windows_test.go` · `TestLiveCacheThisPCIsolated`：真实服务器隔离根新建/编辑/粘贴/拖入/导出/全文回读/两次独立提供方重启通过；测试项按身份清理。
- 本机正式运行版：真实从“此电脑”进入，直接根目录右键新建/编辑/粘贴/Explorer Ctrl拖入、服务器全文回读及默认300秒自动回收通过；不是子目录替代。
- 实際运行宠物固定面板真实Explorer Ctrl拖入通过，Core为cloud_upload/completed、同正式根回读、源文件保留。
- 最终正常托盘退出/更新/重启后宠物与同账号入口恢复，无凭据重输；独立正式云盘窗口556×696窄高，原生状态列可见。
- 本机4个验收文件及对应完成任务/测试状态精确清理；34个原服务器对象ID/版本/全文hash不变。

原始PID、服务器地址、用户文件名与哈希证据留在本机私有目录，不纳入公开源码。

## 发布与回退边界

已切换本机实验运行双程序，不自动提交推送、合并、发Release或升级生产服务器。回退双程序/受限状态已配套保留；不将客户端包称完整服务器迁移包。共享CFAPI、删除改名双向同步、远端更新自动替换已有缓存、动物模型、Windows11多标签/多屏真机、NSIS和本轮Java/Python业务全套仍不属于本次验收。
