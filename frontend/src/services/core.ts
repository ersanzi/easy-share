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
  GetLogDirectory,
  GetSettings,
  GetSnapshot,
  RejectTransfer,
  ReportFrontendError,
  SaveSettings,
  SelectFile,
  SelectFiles,
  SelectReceiveDirectory,
  SelectShareDirectory,
  SendFile,
  ShutdownAll,
  StartDrive,
  StopDrive,
} from '../../wailsjs/go/main/App'
import type { CloudFile, CoreSnapshot } from '../types/core'

export interface SettingsData {
  deviceName: string
  receiveDir: string
  webdavRoot: string
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
}
