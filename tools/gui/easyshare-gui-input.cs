using System;
using System.Drawing;
using System.Threading;
using System.Runtime.InteropServices;
using System.Windows.Forms;
using System.Windows.Automation;
public static class EAInput {
 [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X,Y; public POINT(int x,int y){X=x;Y=y;} }
 [StructLayout(LayoutKind.Sequential)] struct MI {public int dx,dy;public uint data,flags,time;public UIntPtr extra;}
 [StructLayout(LayoutKind.Sequential)] struct KI {public ushort vk,scan;public uint flags,time;public UIntPtr extra;}
 [StructLayout(LayoutKind.Explicit)] struct U { [FieldOffset(0)] public MI mouse; [FieldOffset(0)] public KI key; }
 [StructLayout(LayoutKind.Sequential)] struct INPUT {public uint type;public U u;}
 [DllImport("user32.dll")] static extern uint SendInput(uint n,INPUT[] a,int size);
 [DllImport("user32.dll")] public static extern IntPtr WindowFromPoint(POINT p);
 [DllImport("user32.dll")] public static extern IntPtr GetAncestor(IntPtr h,uint f);
 [DllImport("user32.dll")] public static extern IntPtr GetWindow(IntPtr h,uint f);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll")] static extern bool GetCursorPos(out POINT p);
 [DllImport("user32.dll")] static extern int GetSystemMetrics(int n);
 [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a,uint b,bool attach);
 static bool FocusSource(IntPtr h) {
  uint p;uint current=GetCurrentThreadId(),target=GetWindowThreadProcessId(h,out p),foreground=GetWindowThreadProcessId(GetForegroundWindow(),out p);bool a=false,b=false;
  try {if(current!=foreground&&foreground!=0)a=AttachThreadInput(current,foreground,true);if(current!=target&&target!=foreground)b=AttachThreadInput(current,target,true);SetForegroundWindow(h);}
  finally {if(b)AttachThreadInput(current,target,false);if(a)AttachThreadInput(current,foreground,false);}
  // 输入队列分离可能撤销前台切换，必须在分离完成后验证，不返回瞬时成功。
  Thread.Sleep(80);return GetForegroundWindow()==h;
 }
 [DllImport("user32.dll")] static extern bool IsWindow(IntPtr h);
 [DllImport("user32.dll")] static extern short GetAsyncKeyState(int k);
 [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
 static void KeyInput(ushort vk,ushort scan,uint flags){INPUT a=new INPUT();a.type=1;a.u.key.vk=vk;a.u.key.scan=scan;a.u.key.flags=flags;if(SendInput(1,new INPUT[]{a},Marshal.SizeOf(typeof(INPUT)))!=1)throw new Exception("Keyboard SendInput rejected");}
 public static void Press(IntPtr root,int pid,ushort key){Identity(root,pid);if(GetForegroundWindow()!=root)throw new Exception("Keyboard target not foreground");KeyInput(key,0,0);KeyInput(key,0,2);Thread.Sleep(90);}
 public static void Shortcut(IntPtr root,int pid,ushort key){Identity(root,pid);if(GetForegroundWindow()!=root)throw new Exception("Shortcut target not foreground");KeyInput(0x11,0,0);Thread.Sleep(40);KeyInput(key,0,0);KeyInput(key,0,2);KeyInput(0x11,0,2);Thread.Sleep(150);}
 public static void RenameText(IntPtr root,int pid,string expected,string value){
  Identity(root,pid);var focus=AutomationElement.FocusedElement;
  if(GetForegroundWindow()!=root||focus==null||focus.Current.ProcessId!=pid||!focus.Current.HasKeyboardFocus||focus.Current.ClassName!="UIRenameTextElement"||focus.Current.Name!=expected||!Descendant(focus,AutomationElement.FromHandle(root)))throw new Exception("Rename control identity changed");
  KeyInput(0x11,0,0);KeyInput(0x41,0,0);KeyInput(0x41,0,2);KeyInput(0x11,0,2);Thread.Sleep(120);
  foreach(char ch in value){if(GetForegroundWindow()!=root||!Automation.Compare(AutomationElement.FocusedElement,focus))throw new Exception("Rename focus changed");KeyInput(0,ch,4);KeyInput(0,ch,6);Thread.Sleep(25);}
  Thread.Sleep(150);
  if(focus.Current.Name!=value)throw new Exception("Rename text did not match; Enter withheld");
  Press(root,pid,0x0d);
 }
 static void Emit(uint flags,int x,int y){INPUT a=new INPUT();a.type=0;a.u.mouse.flags=flags;a.u.mouse.dx=x;a.u.mouse.dy=y;if(SendInput(1,new INPUT[]{a},Marshal.SizeOf(typeof(INPUT)))!=1)throw new Exception("SendInput rejected");}
 public static void Identity(IntPtr h,int pid){uint p;if(!IsWindow(h))throw new Exception("HWND lost");GetWindowThreadProcessId(h,out p);if(p!=(uint)pid)throw new Exception("HWND PID changed: hwnd="+h.ToInt64()+" expected="+pid+" actual="+p);}
 public static bool Owned(IntPtr window,IntPtr explorer,int pid){try{Identity(window,pid);IntPtr root=GetAncestor(window,2);for(int i=0;i<12&&root!=IntPtr.Zero;i++){if(root==explorer)return true;root=GetWindow(root,4);}return false;}catch{return false;}}
 public static void Hit(IntPtr root,int pid,int x,int y){Identity(root,pid);IntPtr hit=WindowFromPoint(new POINT(x,y));Identity(hit,pid);if(GetAncestor(hit,2)!=root)throw new Exception("Point occluded or wrong HWND");}
 static bool Descendant(AutomationElement e,AutomationElement parent){for(int i=0;i<40&&e!=null;i++,e=TreeWalker.RawViewWalker.GetParent(e))if(Automation.Compare(e,parent))return true;return false;}
 public static void Background(IntPtr root,int pid,int x,int y){Hit(root,pid,x,y);AutomationElement top=AutomationElement.FromHandle(root);AutomationElement e=AutomationElement.FromPoint(new System.Windows.Point(x,y));bool pane=false;for(int i=0;i<40&&e!=null;i++,e=TreeWalker.RawViewWalker.GetParent(e)){if(e.Current.ProcessId!=pid)throw new Exception("UIA PID mismatch");ControlType t=e.Current.ControlType;if(t==ControlType.ListItem||t==ControlType.DataItem||t==ControlType.Edit||t==ControlType.Button||t==ControlType.TreeItem)throw new Exception("Not empty background");if(e.Current.ClassName=="UIItemsView")pane=true;if(Automation.Compare(e,top)){if(!pane)throw new Exception("Not file pane");return;}}throw new Exception("Not dedicated window descendant");}
 public static void Move(int x,int y){int l=GetSystemMetrics(76),t=GetSystemMetrics(77),w=GetSystemMetrics(78),h=GetSystemMetrics(79);Emit(0xC001,(int)Math.Round((x-l)*65535.0/(w-1)),(int)Math.Round((y-t)*65535.0/(h-1)));POINT p;GetCursorPos(out p);if(Math.Abs(p.X-x)>2||Math.Abs(p.Y-y)>2)throw new Exception("Cursor position mismatch");}
 public static void LeftClickBackground(IntPtr h,int pid,int x,int y){Background(h,pid,x,y);if(GetForegroundWindow()!=h||GetAsyncKeyState(1)<0||GetAsyncKeyState(2)<0)throw new Exception("Unsafe background focus");Move(x,y);Background(h,pid,x,y);Emit(6,0,0);}
 public static void RightClick(IntPtr h,int pid,int x,int y){Background(h,pid,x,y);if(GetForegroundWindow()!=h)throw new Exception("Foreground changed");Move(x,y);Background(h,pid,x,y);Emit(0x18,0,0);}
 public static void DoubleClickItem(IntPtr root,int pid,int x,int y,string expectedName){Identity(root,pid);if(GetForegroundWindow()!=root)throw new Exception("Double-click window not foreground");Hit(root,pid,x,y);AutomationElement e=AutomationElement.FromPoint(new System.Windows.Point(x,y));bool found=false;for(int i=0;i<12&&e!=null;i++,e=TreeWalker.RawViewWalker.GetParent(e))if(e.Current.ControlType==ControlType.ListItem&&e.Current.Name==expectedName&&e.Current.ProcessId==pid){found=true;break;}if(!found)throw new Exception("Double-click exact tile hit unproved");Move(x,y);Hit(root,pid,x,y);Emit(6,0,0);Thread.Sleep(70);Hit(root,pid,x,y);Emit(6,0,0);}
 public static string LastDragDiagnostic="";
 public static string Drag(IntPtr target,int pid,string source,int sx,int sy,int tx,int ty){
  LastDragDiagnostic="start";
  Background(target,pid,sx,sy);Background(target,pid,tx,ty);
  if(GetAsyncKeyState(1)<0||GetAsyncKeyState(2)<0)throw new Exception("Mouse already held");
  string result="notAccepted";bool started=false,cancel=false;Thread driver=null;
  using(Form f=new Form())using(Button b=new Button()){
   f.Text="EasyShare automatic OLE source";f.TopMost=true;f.FormBorderStyle=FormBorderStyle.None;f.AutoScaleMode=AutoScaleMode.None;f.MinimumSize=new Size(1,1);f.MaximumSize=new Size(50,40);f.ShowInTaskbar=false;f.StartPosition=FormStartPosition.Manual;f.Bounds=new Rectangle(sx-25,sy-20,50,40);b.Dock=DockStyle.Fill;b.Text="OLE";f.Controls.Add(b);
   IntPtr sourceH=IntPtr.Zero;int own=System.Diagnostics.Process.GetCurrentProcess().Id;
   b.QueryContinueDrag+=(s,e)=>{if(cancel)e.Action=DragAction.Cancel;};
   b.MouseDown+=(s,e)=>{if(e.Button!=MouseButtons.Left)return;try{Identity(target,pid);started=true;DataObject data=new DataObject();var files=new System.Collections.Specialized.StringCollection();files.Add(source);data.SetFileDropList(files);DragDropEffects effect=b.DoDragDrop(data,DragDropEffects.Copy);if(!cancel)result=effect.ToString();}catch{result="OLEFailed";}finally{f.BeginInvoke((Action)(()=>f.Close()));}};
   f.Shown+=(s,e)=>{sourceH=f.Handle;driver=new Thread(()=>{
    bool down=false;
    try{
     LastDragDiagnostic="source-visible";Thread.Sleep(200);Identity(target,pid);Hit(sourceH,own,sx,sy);Background(target,pid,tx,ty);
     // 让源窗口所属 UI 线程完成激活，再确认稳定前台；按下鼠标前仍严格检查身份。
     bool focused=false;
     for(int attempt=0;attempt<3&&!focused;attempt++){
      f.Invoke((Action)(()=>{f.Activate();b.Focus();}));Thread.Sleep(100);
      focused=GetForegroundWindow()==sourceH||FocusSource(sourceH);
     }
     if(!focused)throw new Exception("Source foreground failed after UI activation");
     Move(sx,sy);Hit(sourceH,own,sx,sy);if(GetForegroundWindow()!=sourceH)throw new Exception("Source focus changed");Emit(2,0,0);down=true;
     for(int n=1;n<=24;n++){
      LastDragDiagnostic="moving-step-"+n;Thread.Sleep(35);Identity(target,pid);Identity(sourceH,own);
      IntPtr fg=GetForegroundWindow();if(fg!=sourceH&&fg!=target)throw new Exception("Foreign foreground");
      int x=sx+(tx-sx)*n/24,y=sy+(ty-sy)*n/24;IntPtr hitRoot=GetAncestor(WindowFromPoint(new POINT(x,y)),2);
      if(hitRoot==sourceH)Hit(sourceH,own,x,y);else Background(target,pid,x,y);
      Move(x,y);
      if(hitRoot==sourceH)Hit(sourceH,own,x,y);else Background(target,pid,x,y);
     }
     if(!started)throw new Exception("OLE never started");Background(target,pid,tx,ty);Emit(4,0,0);down=false;
    }catch(Exception err){cancel=true;LastDragDiagnostic=LastDragDiagnostic+" | "+err.GetType().Name+": "+err.Message;result="guardFailed-notAccepted";
     // Cancel the owned OLE loop BEFORE releasing, never drop on an unverified window.
     if(down){try{Identity(sourceH,own);Hit(sourceH,own,sx,sy);Move(sx,sy);Thread.Sleep(150);Hit(sourceH,own,sx,sy);Emit(4,0,0);down=false;}catch{result="guardFailed-inputReleaseUnproved";}}
     try{f.BeginInvoke((Action)(()=>f.Close()));}catch{}
    }
   });driver.IsBackground=true;driver.Start();};
   using(var timer=new System.Windows.Forms.Timer()){timer.Interval=12000;timer.Tick+=(s,e)=>{cancel=true;result="timeout-notAccepted";f.Close();};timer.Start();Application.Run(f);}
   if(driver!=null)driver.Join(1500);
  }
  return result;
 }
}
