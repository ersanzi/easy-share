using System;using System.Collections.Generic;using System.Runtime.InteropServices;using System.Text;
// 只读取已核实Explorer窗口内的SysTreeView32；远程内存仅容纳TVITEM/文本，不执行代码。
public static class OwnedExplorerTree {
 [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr parent,EnumChild callback,IntPtr data);
 delegate bool EnumChild(IntPtr h,IntPtr data);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr h,StringBuilder text,int n);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h,out uint pid);
 [DllImport("user32.dll")] static extern IntPtr SendMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access,bool inherit,uint pid);
 [DllImport("kernel32.dll")] static extern IntPtr VirtualAllocEx(IntPtr p,IntPtr at,UIntPtr n,uint allocation,uint protect);
 [DllImport("kernel32.dll")] static extern bool VirtualFreeEx(IntPtr p,IntPtr at,UIntPtr n,uint kind);
 [DllImport("kernel32.dll")] static extern bool WriteProcessMemory(IntPtr p,IntPtr at,byte[] data,UIntPtr n,out UIntPtr written);
 [DllImport("kernel32.dll")] static extern bool ReadProcessMemory(IntPtr p,IntPtr at,byte[] data,UIntPtr n,out UIntPtr read);
 [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr p);
 [DllImport("user32.dll")] static extern bool ClientToScreen(IntPtr h,ref Point p);
 [StructLayout(LayoutKind.Sequential)] struct Point{public int X,Y;}
 [StructLayout(LayoutKind.Sequential)] struct Item{public uint Mask;public IntPtr Handle;public uint State,StateMask;public IntPtr Text;public int TextMax,Image,SelectedImage,Children;public IntPtr Param;}
 public class Row{public long Handle,Parent;public string Name;public int X,Y;}
 public static Row[] Read(IntPtr explorer,uint expected){
  uint owner;GetWindowThreadProcessId(explorer,out owner);if(owner!=expected)throw new Exception("Wrong Explorer owner");
  IntPtr tree=IntPtr.Zero;EnumChildWindows(explorer,(h,d)=>{var t=new StringBuilder(100);GetClassName(h,t,100);if(t.ToString()=="SysTreeView32")tree=h;return true;},IntPtr.Zero);
  if(tree==IntPtr.Zero)throw new Exception("Native navigation tree unavailable");
  IntPtr p=OpenProcess(0x38,false,owner);if(p==IntPtr.Zero)throw new Exception("Cannot query owned tree process");
  IntPtr memory=VirtualAllocEx(p,IntPtr.Zero,(UIntPtr)4096,0x3000,4);if(memory==IntPtr.Zero){CloseHandle(p);throw new Exception("Tree query buffer failed");}
  var rows=new List<Row>();
  try {Walk(tree,p,memory,SendMessage(tree,0x110A,IntPtr.Zero,IntPtr.Zero),IntPtr.Zero,rows);return rows.ToArray();}
  finally{VirtualFreeEx(p,memory,UIntPtr.Zero,0x8000);CloseHandle(p);}
 }
 static void Walk(IntPtr tree,IntPtr p,IntPtr memory,IntPtr item,IntPtr parent,List<Row> rows){
  int guard=0;while(item!=IntPtr.Zero&&guard++<300&&rows.Count<600){
   var v=new Item{Mask=1,Handle=item,Text=IntPtr.Add(memory,512),TextMax=1024};int size=Marshal.SizeOf(v);IntPtr local=Marshal.AllocHGlobal(size);
   byte[] data=new byte[size];try{Marshal.StructureToPtr(v,local,false);Marshal.Copy(local,data,0,size);}finally{Marshal.FreeHGlobal(local);}UIntPtr n;
   if(!WriteProcessMemory(p,memory,data,(UIntPtr)data.Length,out n))throw new Exception("Tree request write failed");
   if(SendMessage(tree,0x113E,IntPtr.Zero,memory)==IntPtr.Zero)throw new Exception("TVM_GETITEM failed");
   byte[] text=new byte[2048];if(!ReadProcessMemory(p,IntPtr.Add(memory,512),text,(UIntPtr)text.Length,out n))throw new Exception("Tree text read failed");string name=Encoding.Unicode.GetString(text).Split('\0')[0];
   byte[] rect=new byte[16];Buffer.BlockCopy(BitConverter.GetBytes(item.ToInt64()),0,rect,0,8);WriteProcessMemory(p,memory,rect,(UIntPtr)rect.Length,out n);SendMessage(tree,0x1104,(IntPtr)1,memory);ReadProcessMemory(p,memory,rect,(UIntPtr)rect.Length,out n);
   var point=new Point{X=(BitConverter.ToInt32(rect,0)+BitConverter.ToInt32(rect,8))/2,Y=(BitConverter.ToInt32(rect,4)+BitConverter.ToInt32(rect,12))/2};ClientToScreen(tree,ref point);
   rows.Add(new Row{Handle=item.ToInt64(),Parent=parent.ToInt64(),Name=name,X=point.X,Y=point.Y});
   Walk(tree,p,memory,SendMessage(tree,0x110A,(IntPtr)4,item),item,rows);
   item=SendMessage(tree,0x110A,(IntPtr)1,item);
  }
 }
}
