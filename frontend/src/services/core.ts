import {
  AcceptTransfer,
  GetLogDirectory,
  GetSnapshot,
  MapDrive,
  RejectTransfer,
  ReportFrontendError,
  SelectFile,
  SendFile,
  ShutdownAll,
  StartDrive,
  StopDrive,
  UnmapDrive,
} from '../../wailsjs/go/main/App'
import type { CoreSnapshot } from '../types/core'

export const core = {
  snapshot: () => GetSnapshot() as Promise<CoreSnapshot>,
  selectFile: SelectFile,
  send: SendFile,
  accept: AcceptTransfer,
  reject: RejectTransfer,
  startDrive: StartDrive,
  stopDrive: StopDrive,
  mapDrive: MapDrive,
  unmapDrive: UnmapDrive,
  shutdown: ShutdownAll,
  logDirectory: GetLogDirectory,
  reportError: ReportFrontendError,
}
