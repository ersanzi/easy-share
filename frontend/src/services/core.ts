import {
  AcceptTransfer,
  AcceptTransferAs,
  GetLogDirectory,
  GetSettings,
  GetSnapshot,
  MapDrive,
  RejectTransfer,
  ReportFrontendError,
  SaveSettings,
  SelectFile,
  SelectReceiveDirectory,
  SelectShareDirectory,
  SendFile,
  ShutdownAll,
  StartDrive,
  StopDrive,
  UnmapDrive,
} from '../../wailsjs/go/main/App'
import type { CoreSnapshot } from '../types/core'

export interface SettingsData {
  deviceName: string
  receiveDir: string
  webdavRoot: string
  driveLetter: string
}

export const core = {
  snapshot: () => GetSnapshot() as Promise<CoreSnapshot>,
  selectFile: SelectFile,
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
}
