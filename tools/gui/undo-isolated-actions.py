"""仅对本轮新建隔离CFAPI根执行真实Explorer Delete→Ctrl+Z，不调用服务器。"""
from pathlib import Path
import ctypes, datetime, json, os, subprocess, sys, time
import pythoncom, win32com.client, win32gui, win32process
from pywinauto import Desktop
from pywinauto.keyboard import send_keys

pythoncom.CoInitialize()
manifest = Path(sys.argv[1]).resolve()
m = json.loads(manifest.read_text(encoding='utf-8-sig'))
root = Path(m['root']).resolve()
run = Path(m['run']).resolve()
assert root.parent == run and run.name.startswith('undo-probe-'), '只允许本轮隔离根'
assert (run / 'manifest.json').resolve() == manifest
assert str(run.parent).replace('\\', '/').endswith('/work/explorer-pet/.rd-test')
assert all((root / name).exists() for name in m['items'])
shell = win32com.client.Dispatch('Shell.Application')
old = {int(window.HWND) for window in shell.Windows()}
subprocess.Popen(['explorer.exe', '/n,', str(root)])
window = None
for _ in range(60):
    time.sleep(.15)
    pythoncom.PumpWaitingMessages()
    for candidate in shell.Windows():
        try:
            if int(candidate.HWND) not in old and os.path.normcase(os.path.normpath(str(candidate.Document.Folder.Self.Path))) == os.path.normcase(str(root)):
                assert window is None or int(window.HWND) == int(candidate.HWND), '隔离窗口不唯一'
                window = candidate
        except (AttributeError, pythoncom.com_error):
            continue
    if window is not None:
        break
assert window is not None, '隔离Explorer窗口未出现'
hwnd = int(window.HWND)
_, pid = win32process.GetWindowThreadProcessId(hwnd)
wrapper = Desktop(backend='uia').window(handle=hwnd)
proof = {'input': 'actual Explorer Delete and Ctrl+Z', 'root': str(root), 'cases': [], 'trace': [], 'serverCalled': False}


def trace(phase, name):
    proof['trace'].append({'phase': phase, 'name': name, 'exists': (root / name).exists(), 'at': time.monotonic(), 'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'foreground': win32gui.GetForegroundWindow()})
    (run / 'input-trace.json').write_text(json.dumps(proof, ensure_ascii=False, indent=2), encoding='utf-8')


def guard():
    assert win32gui.IsWindow(hwnd)
    assert win32process.GetWindowThreadProcessId(hwnd)[1] == pid
    assert os.path.normcase(os.path.normpath(str(window.Document.Folder.Self.Path))) == os.path.normcase(str(root))
    assert win32gui.GetForegroundWindow() == hwnd, '目标窗口失去前台，停止输入'


def wait_path(path, exists):
    for _ in range(80):
        if path.exists() == exists:
            return
        time.sleep(.1)
    raise RuntimeError('文件恢复/删除未达到目标状态: ' + path.name)


try:
    wrapper.restore()
    wrapper.maximize()
    wrapper.set_focus()
    time.sleep(.7)
    for name in m['items']:
        wrapper.set_focus()
        guard()
        matches = [item for item in window.Document.Folder.Items() if os.path.normcase(os.path.normpath(str(item.Path))) == os.path.normcase(str(root / name))]
        assert len(matches) == 1, '精确目标不唯一'
        window.Document.SelectItem(matches[0], 29)
        time.sleep(.25)
        targets = [d for d in wrapper.descendants(control_type='ListItem') if d.window_text() in (name, name.rsplit('.', 1)[0])]
        assert len(targets) == 1, '列表鼠标目标不唯一'
        targets[0].click_input()
        time.sleep(.1)
        selected = [str(item.Path) for item in window.Document.SelectedItems()]
        assert len(selected) == 1 and os.path.normcase(os.path.normpath(selected[0])) == os.path.normcase(str(root / name)), '选中目标身份错误'
        guard()
        started = time.monotonic()
        trace('before-delete', name)
        if m.get('menuRequest') and name != '未上传.txt':
            response_path = run / 'menu-response.json'
            if response_path.exists():
                response_path.unlink()
            (run / 'menu-request.json').write_text(json.dumps({'path': str(root / name)}, ensure_ascii=False), encoding='utf-8')
            for _ in range(200):
                if response_path.exists():
                    response = json.loads(response_path.read_text(encoding='utf-8'))
                    assert response['path'] == str(root / name), '菜单响应目标不一致'
                    assert not response['error'], response['error']
                    break
                time.sleep(.1)
            else:
                raise RuntimeError('隔离菜单回收请求超时')
        else:
            send_keys('{DELETE}', pause=.05)
        (run / 'delete-issued').write_text(name, encoding='utf-8')
        time.sleep(.7)
        trace('after-delete-input', name)
        if (root / name).exists():
            windows = Desktop(backend='uia').windows()
            diagnostic = [{'handle': d.handle, 'pid': d.element_info.process_id, 'title': d.window_text(), 'owner': win32gui.GetWindow(d.handle, 4), 'class': d.class_name()} for d in windows if d.is_visible()]
            (run / 'dialog-diagnostic.json').write_text(json.dumps(diagnostic, ensure_ascii=False, indent=2), encoding='utf-8')
            def owned(d):
                owner = win32gui.GetWindow(d.handle, 4)
                for _ in range(10):
                    if owner == hwnd:
                        return True
                    if not owner:
                        return False
                    owner = win32gui.GetWindow(owner, 4)
                return False
            dialogs = [d for d in windows if d.element_info.process_id == pid and d.handle != hwnd and d.is_visible() and d.class_name() == '#32770' and d.window_text() in ('删除文件', '删除文件夹', 'Delete File', 'Delete Folder', '删除多个项目')]
            assert len(dialogs) == 1, '精确Explorer进程的删除对话框不唯一，停止'
            dialog = dialogs[0]
            labels = [d.window_text() for d in dialog.descendants(control_type='Text')]
            assert any(name in text or name.rsplit('.', 1)[0] in text for text in labels), '确认框未显示精确测试名称，停止'
            buttons = [b for b in dialog.descendants(control_type='Button') if b.window_text() in ('是(Y)', '是(&Y)', '是', 'Yes', '&Yes')]
            assert len(buttons) == 1, '确认按钮不唯一'
            buttons[0].click_input()
        wait_path(root / name, False)
        deleted = time.monotonic()
        wrapper.set_focus()
        guard()
        panes = [d for d in wrapper.descendants() if d.class_name() == 'UIItemsView']
        assert len(panes) == 1, '文件列表未找到'
        rect = panes[0].rectangle()
        targets = [d.rectangle() for d in panes[0].descendants(control_type='ListItem')]
        point = None
        for y in range(rect.bottom - 18, rect.top + 8, -25):
            x = rect.right - 25
            if not any(r.left <= x <= r.right and r.top <= y <= r.bottom for r in targets):
                point = (x - rect.left, y - rect.top)
                break
        assert point is not None, '无安全列表空白点'
        panes[0].click_input(coords=point)
        guard()
        trace('before-undo', name)
        (run / 'undo-issued').write_text(name, encoding='utf-8')
        send_keys('^z', pause=.05)
        wait_path(root / name, True)
        trace('after-undo', name)
        guard()
        proof['cases'].append({'name': name, 'deleteMS': round((deleted-started)*1000), 'undoMS': round((time.monotonic()-deleted)*1000), 'actualUndo': True})
        time.sleep(.3)
    wrapper.capture_as_image().save(str(run / 'undo-view.png'))
    (run / 'gui-result.json').write_text(json.dumps(proof, ensure_ascii=False, indent=2), encoding='utf-8')
    print('ISOLATED_EXPLORER_UNDO_INPUT_PASS')
except Exception:
    try:
        wrapper.capture_as_image().save(str(run / 'undo-failed.png'))
    except Exception:
        pass
    (run / 'gui-failed.json').write_text(json.dumps(proof, ensure_ascii=False, indent=2), encoding='utf-8')
    raise
finally:
    windows = Desktop(backend='uia').windows()
    for d in windows:
        if d.element_info.process_id == pid and d.class_name() == '#32770' and d.window_text() in ('删除文件', '删除文件夹', 'Delete File', 'Delete Folder'):
            win32gui.PostMessage(d.handle, 0x100, 0x1b, 0)
            win32gui.PostMessage(d.handle, 0x101, 0x1b, 0)
    diagnostic_path = run / 'dialog-diagnostic.json'
    if diagnostic_path.exists():
        records = json.loads(diagnostic_path.read_text(encoding='utf-8'))
        for saved in records:
            if saved['class'] != 'OperationStatusWindow':
                continue
            candidates = [d for d in windows if d.handle == saved['handle'] and d.element_info.process_id == saved['pid'] and d.class_name() == saved['class'] and d.window_text() == saved['title']]
            if len(candidates) == 1:
                dialog = candidates[0]
                texts = [d.window_text() for d in dialog.descendants()]
                if any(name in text for name in m['items'] for text in texts):
                    buttons = [d for d in dialog.descendants(control_type='Button') if d.window_text() in ('取消', 'Cancel')]
                    if len(buttons) == 1:
                        buttons[0].click_input()
    if win32gui.IsWindow(hwnd) and win32process.GetWindowThreadProcessId(hwnd)[1] == pid:
        win32gui.PostMessage(hwnd, 0x10, 0, 0)
    pythoncom.CoUninitialize()
