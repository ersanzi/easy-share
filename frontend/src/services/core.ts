import {
  AcceptTransfer,
  AcceptTransferAs,
  ClearHistory,
  CloudDelete,
  CloudDownload,
  CloudList,
  CloudShare,
  CloudUpload,
  DeleteTask,
  GetCloudSettings,
  GetLogDirectory,
  GetSettings,
  GetSnapshot,
  MapDrive,
  RejectTransfer,
  ReportFrontendError,
  SaveCloudSettings,
  SaveSettings,
  SelectFile,
  SelectFiles,
  SelectReceiveDirectory,
  SelectShareDirectory,
  SendFile,
  ShutdownAll,
  StartDrive,
  StopDrive,
  UnmapDrive,
} from '../../wailsjs/go/main/App'
import type { CloudFile, CoreSnapshot } from '../types/core'

export interface SettingsData {
  deviceName: string
  receiveDir: string
  webdavRoot: string
  driveLetter: string
}

export interface CloudSettingsData {
  endpoint: string
  region: string
  accessKeyId: string
  secretAccessKey: string
  bucket: string
  allowInsecureHttp: boolean
}

export const core = {
  snapshot: () => GetSnapshot() as Promise<CoreSnapshot>,
  selectFile: SelectFile,
  selectFiles: SelectFiles,
  send: SendFile,
  accept: AcceptTransfer,
  acceptAs: AcceptTransferAs,
  reject: RejectTransfer,
  startDrive: StartDrive,
  stopDrive: StopDrive,
  mapDrive: MapDrive,
  unmapDrive: UnmapDrive,
  shutdown: ShutdownAll,
  logDirectory: GetLogDirectory,
  reportError: ReportFrontendError,
  getSettings: () => GetSettings() as Promise<SettingsData>,
  saveSettings: SaveSettings,
  selectReceiveDirectory: SelectReceiveDirectory,
  selectShareDirectory: SelectShareDirectory,
  clearHistory: ClearHistory,
  deleteTask: DeleteTask,
  cloudList: () => CloudList() as Promise<CloudFile[]>,
  cloudUpload: CloudUpload,
  cloudDownload: CloudDownload,
  cloudDelete: CloudDelete,
  cloudShare: CloudShare,
  getCloudSettings: () => GetCloudSettings() as Promise<CloudSettingsData>,
  saveCloudSettings: SaveCloudSettings,
}
