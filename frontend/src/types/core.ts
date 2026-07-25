export interface ServiceStatus { core:boolean; discovery:boolean; receiver:boolean; webdav:boolean; cloudEnabled:boolean }
export interface Peer { deviceId:string; deviceName:string; ip:string; transferPort:number; lastSeen:string }
export interface TransferTask { id:string; fileName:string; direction:'send'|'receive'; peer:string; totalBytes:number; transferredBytes:number; speed:number; status:string; error?:string; createdAt?:string; updatedAt?:string }
export type PreviewKind = 'unsupported' | 'image' | 'pdf' | 'text'
export interface CloudFile { key:string; name:string; size:number; contentType:string; lastModified:string; previewKind:PreviewKind }
export interface CloudPreview { key:string; name:string; kind:PreviewKind; contentType:string; size:number; contentUrl?:string; text?:string; truncated?:boolean }
export interface DroppedFiles { files:string[]; dirs:string[] }
export interface CoreSnapshot { status:ServiceStatus; peers:Peer[]; tasks:TransferTask[] }
