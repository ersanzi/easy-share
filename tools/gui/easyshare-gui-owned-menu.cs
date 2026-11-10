using System;
using System.Text;
using System.Threading;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class EAOwnedMenu {
 [StructLayout(LayoutKind.Sequential)] public struct RECT {public int l,t,r,b;}
 [StructLayout(LayoutKind.Sequential)] struct POINT {public int x,y;}
 [StructLayout(LayoutKind.Sequential)] struct GI {public uint size,flags;public IntPtr active,focus,capture,owner,move,caret;public RECT rect;}
 [StructLayout(LayoutKind.Sequential)] struct MB {public uint size;public RECT rect;public IntPtr menu,window;public uint flags;}
 delegate bool EnumProc(IntPtr h,IntPtr l);
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc f,IntPtr l);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h,out uint p);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h,StringBuilder b,int n);
 [DllImport("user32.dll")] static extern bool GetGUIThreadInfo(uint t,ref GI info);
 [DllImport("user32.dll")] static extern bool GetMenuBarInfo(IntPtr h,int obj,int item,ref MB b);
 [DllImport("user32.dll")] static extern int GetMenuItemCount(IntPtr menu);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetMenuString(IntPtr menu,uint item,StringBuilder b,int n,uint flags);
 [DllImport("user32.dll")] static extern uint GetMenuItemID(IntPtr menu,int item);
 [DllImport("user32.dll")] static extern uint GetMenuState(IntPtr menu,uint item,uint flags);
 [DllImport("user32.dll")] static extern IntPtr GetSubMenu(IntPtr menu,int item);
 [DllImport("user32.dll")] static extern bool GetMenuItemRect(IntPtr window,IntPtr menu,uint item,out RECT r);
 [DllImport("user32.dll")] static extern IntPtr GetAncestor(IntPtr h,uint flag);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] static extern IntPtr WindowFromPoint(POINT p);
 [DllImport("user32.dll")] static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] static extern short GetAsyncKeyState(int key);
 [DllImport("user32.dll")] static extern void mouse_event(uint f,uint x,uint y,uint data,UIntPtr extra);
 public class Item {public long Window,Menu,Owner;public uint PID,Thread,ID,State;public int Index;public string Name,Normalized;public bool Submenu;public RECT Rect;}
 static string Normalize(string text){return System.Text.RegularExpressions.Regex.Replace(text,@"\((?:&)?[A-Za-z]\)|&","").Trim();}
 public static Item[] Snapshot(IntPtr target,uint pid){
  uint p;uint tid=GetWindowThreadProcessId(target,out p);if(p!=pid)throw new Exception("Target identity changed");
  var g=new GI();g.size=(uint)Marshal.SizeOf(typeof(GI));if(!GetGUIThreadInfo(tid,ref g)||g.owner==IntPtr.Zero||(g.owner!=target&&GetAncestor(g.owner,2)!=target))throw new Exception("Menu owner outside dedicated Explorer");
  var items=new List<Item>();
  EnumWindows((h,l)=>{uint hp;uint ht=GetWindowThreadProcessId(h,out hp);var cls=new StringBuilder(256);GetClassName(h,cls,256);if(hp!=pid||ht!=tid||cls.ToString()!="#32768"||!IsWindowVisible(h))return true;
   var b=new MB();b.size=(uint)Marshal.SizeOf(typeof(MB));if(!GetMenuBarInfo(h,-4,0,ref b)||b.menu==IntPtr.Zero)return true;
   for(int i=0;i<GetMenuItemCount(b.menu);i++){var name=new StringBuilder(1024);GetMenuString(b.menu,(uint)i,name,1024,0x400);RECT r;if(!GetMenuItemRect(h,b.menu,(uint)i,out r))continue;
    items.Add(new Item{Window=h.ToInt64(),Menu=b.menu.ToInt64(),Owner=g.owner.ToInt64(),PID=pid,Thread=tid,Index=i,ID=GetMenuItemID(b.menu,i),State=GetMenuState(b.menu,(uint)i,0x400),Name=name.ToString(),Normalized=Normalize(name.ToString()),Submenu=GetSubMenu(b.menu,i)!=IntPtr.Zero,Rect=r});}
   return true;
  },IntPtr.Zero);return items.ToArray();
 }
 public static Item Click(IntPtr target,uint pid,string expected,bool submenu){
  var matches=new List<Item>();foreach(var item in Snapshot(target,pid))if(item.Normalized==expected&&item.Submenu==submenu&&(item.State&3)==0)matches.Add(item);
  if(matches.Count!=1)throw new Exception("Exact enabled native menu item not unique");var x=matches[0];
  var point=new POINT{x=(x.Rect.l+x.Rect.r)/2,y=(x.Rect.t+x.Rect.b)/2};IntPtr hwnd=new IntPtr(x.Window);
  if(WindowFromPoint(point)!=hwnd||GetAsyncKeyState(1)<0||GetAsyncKeyState(2)<0||!SetCursorPos(point.x,point.y))throw new Exception("Native menu hit/idle check failed");
  Thread.Sleep(120);
  if(WindowFromPoint(point)!=hwnd)throw new Exception("Native menu now occluded");
  var verify=Snapshot(target,pid);bool same=false;foreach(var item in verify)if(item.Window==x.Window&&item.Menu==x.Menu&&item.Index==x.Index&&item.Name==x.Name&&(item.State&3)==0)same=true;
  if(!same)throw new Exception("Native menu changed before click");mouse_event(6,0,0,0,UIntPtr.Zero);Thread.Sleep(500);return x;
 }
}
