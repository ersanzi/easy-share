export namespace account {
	
	export class Capacity {
	    enabled: boolean;
	    usableBytes: number;
	    poolBytes: number;
	    reservedBytes: number;
	    committedBytes: number;
	    usedBytes: number;
	    unlimitedCount: number;
	
	    static createFrom(source: any = {}) {
	        return new Capacity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.usableBytes = source["usableBytes"];
	        this.poolBytes = source["poolBytes"];
	        this.reservedBytes = source["reservedBytes"];
	        this.committedBytes = source["committedBytes"];
	        this.usedBytes = source["usedBytes"];
	        this.unlimitedCount = source["unlimitedCount"];
	    }
	}
	export class ManagedUser {
	    userId: string;
	    userName: string;
	    nickName: string;
	    deptName: string;
	    status: string;
	    createTime: string;
	    loginDate: string;
	
	    static createFrom(source: any = {}) {
	        return new ManagedUser(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userId = source["userId"];
	        this.userName = source["userName"];
	        this.nickName = source["nickName"];
	        this.deptName = source["deptName"];
	        this.status = source["status"];
	        this.createTime = source["createTime"];
	        this.loginDate = source["loginDate"];
	    }
	}
	export class NewUser {
	    userName: string;
	    nickName: string;
	    password: string;
	    deptId?: string;
	    roleIds?: string[];
	
	    static createFrom(source: any = {}) {
	        return new NewUser(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userName = source["userName"];
	        this.nickName = source["nickName"];
	        this.password = source["password"];
	        this.deptId = source["deptId"];
	        this.roleIds = source["roleIds"];
	    }
	}
	export class Space {
	    spaceId: string;
	    spaceType: string;
	    ownerId: string;
	    spaceName: string;
	    quotaBytes: number;
	    usedBytes: number;
	    status: string;
	    permission: string;
	
	    static createFrom(source: any = {}) {
	        return new Space(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.spaceId = source["spaceId"];
	        this.spaceType = source["spaceType"];
	        this.ownerId = source["ownerId"];
	        this.spaceName = source["spaceName"];
	        this.quotaBytes = source["quotaBytes"];
	        this.usedBytes = source["usedBytes"];
	        this.status = source["status"];
	        this.permission = source["permission"];
	    }
	}
	export class UserPage {
	    total: number;
	    rows: ManagedUser[];
	
	    static createFrom(source: any = {}) {
	        return new UserPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.rows = this.convertValues(source["rows"], ManagedUser);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace api {
	
	export class Status {
	    core: boolean;
	    discovery: boolean;
	    receiver: boolean;
	    webdav: boolean;
	    cloudEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.core = source["core"];
	        this.discovery = source["discovery"];
	        this.receiver = source["receiver"];
	        this.webdav = source["webdav"];
	        this.cloudEnabled = source["cloudEnabled"];
	    }
	}

}

export namespace cloud {
	
	export class File {
	    key: string;
	    name: string;
	    size: number;
	    contentType: string;
	    // Go type: time
	    lastModified: any;
	    previewKind: string;
	
	    static createFrom(source: any = {}) {
	        return new File(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.contentType = source["contentType"];
	        this.lastModified = this.convertValues(source["lastModified"], null);
	        this.previewKind = source["previewKind"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Preview {
	    key: string;
	    name: string;
	    kind: string;
	    contentType: string;
	    size: number;
	    contentUrl?: string;
	    text?: string;
	    truncated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Preview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.contentType = source["contentType"];
	        this.size = source["size"];
	        this.contentUrl = source["contentUrl"];
	        this.text = source["text"];
	        this.truncated = source["truncated"];
	    }
	}

}

export namespace desktop {
	
	export class Snapshot {
	    status: api.Status;
	    peers: discovery.Peer[];
	    tasks: task.Task[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], api.Status);
	        this.peers = this.convertValues(source["peers"], discovery.Peer);
	        this.tasks = this.convertValues(source["tasks"], task.Task);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace discovery {
	
	export class Peer {
	    deviceId: string;
	    deviceName: string;
	    ip: string;
	    transferPort: number;
	    // Go type: time
	    lastSeen: any;
	
	    static createFrom(source: any = {}) {
	        return new Peer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceId = source["deviceId"];
	        this.deviceName = source["deviceName"];
	        this.ip = source["ip"];
	        this.transferPort = source["transferPort"];
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace fsutil {
	
	export class DriveInfo {
	    letter: string;
	    label: string;
	    type: string;
	    totalBytes: number;
	    freeBytes: number;
	    usedPct: number;
	
	    static createFrom(source: any = {}) {
	        return new DriveInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.letter = source["letter"];
	        this.label = source["label"];
	        this.type = source["type"];
	        this.totalBytes = source["totalBytes"];
	        this.freeBytes = source["freeBytes"];
	        this.usedPct = source["usedPct"];
	    }
	}
	export class FileEntry {
	    name: string;
	    path: string;
	    isDir: boolean;
	    size: number;
	    // Go type: time
	    modTime: any;
	    ext: string;
	
	    static createFrom(source: any = {}) {
	        return new FileEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.modTime = this.convertValues(source["modTime"], null);
	        this.ext = source["ext"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace knowledge {
	
	export class Context {
	    doc_id?: string;
	    file_id?: string;
	    version_id?: string;
	    filename?: string;
	    score?: number;
	    ingested_at?: string;
	    text: string;
	    block_ids: string[];
	
	    static createFrom(source: any = {}) {
	        return new Context(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.doc_id = source["doc_id"];
	        this.file_id = source["file_id"];
	        this.version_id = source["version_id"];
	        this.filename = source["filename"];
	        this.score = source["score"];
	        this.ingested_at = source["ingested_at"];
	        this.text = source["text"];
	        this.block_ids = source["block_ids"];
	    }
	}
	export class SourceRef {
	    doc_id?: string;
	    score?: number;
	    ingested_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new SourceRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.doc_id = source["doc_id"];
	        this.score = source["score"];
	        this.ingested_at = source["ingested_at"];
	    }
	}
	export class Answer {
	    answer: string;
	    sources: SourceRef[];
	    contexts: Context[];
	
	    static createFrom(source: any = {}) {
	        return new Answer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.answer = source["answer"];
	        this.sources = this.convertValues(source["sources"], SourceRef);
	        this.contexts = this.convertValues(source["contexts"], Context);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Health {
	    records: number;
	    llm: string;
	    watch_dirs: number;
	
	    static createFrom(source: any = {}) {
	        return new Health(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.records = source["records"];
	        this.llm = source["llm"];
	        this.watch_dirs = source["watch_dirs"];
	    }
	}
	
	export class StatusView {
	    configured: boolean;
	    loggedIn: boolean;
	    serverUrl: string;
	    username: string;
	    role: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.loggedIn = source["loggedIn"];
	        this.serverUrl = source["serverUrl"];
	        this.username = source["username"];
	        this.role = source["role"];
	    }
	}

}

export namespace main {
	
	export class AuthUser {
	    loggedIn: boolean;
	    userName: string;
	    nickName: string;
	    avatar: string;
	    isAdmin: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AuthUser(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.loggedIn = source["loggedIn"];
	        this.userName = source["userName"];
	        this.nickName = source["nickName"];
	        this.avatar = source["avatar"];
	        this.isAdmin = source["isAdmin"];
	    }
	}
	export class FilesDroppedEvent {
	    files: string[];
	    dirs: string[];
	
	    static createFrom(source: any = {}) {
	        return new FilesDroppedEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.dirs = source["dirs"];
	    }
	}
	export class PluginInvokeResult {
	    ok: boolean;
	    data?: number[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new PluginInvokeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.data = source["data"];
	        this.error = source["error"];
	    }
	}
	export class Settings {
	    deviceName: string;
	    receiveDir: string;
	    webdavRoot: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceName = source["deviceName"];
	        this.receiveDir = source["receiveDir"];
	        this.webdavRoot = source["webdavRoot"];
	    }
	}
	export class UpdateAssetInfo {
	    id: string;
	    kind: string;
	    filename: string;
	    size: number;
	    sha256: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateAssetInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.filename = source["filename"];
	        this.size = source["size"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class UpdateCheckResult {
	    currentVersion: string;
	    latestVersion: string;
	    hasUpdate: boolean;
	    notes: string;
	    publishedAt: string;
	    asset?: UpdateAssetInfo;
	    installedMode: boolean;
	    canAutoInstall: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.hasUpdate = source["hasUpdate"];
	        this.notes = source["notes"];
	        this.publishedAt = source["publishedAt"];
	        this.asset = this.convertValues(source["asset"], UpdateAssetInfo);
	        this.installedMode = source["installedMode"];
	        this.canAutoInstall = source["canAutoInstall"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace plugin {
	
	export class Info {
	    id: string;
	    name: string;
	    version: string;
	    description: string;
	    icon: string;
	    entry: string;
	    builtin: boolean;
	    disabled: boolean;
	    permissions: string[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.description = source["description"];
	        this.icon = source["icon"];
	        this.entry = source["entry"];
	        this.builtin = source["builtin"];
	        this.disabled = source["disabled"];
	        this.permissions = source["permissions"];
	    }
	}
	export class MarketAsset {
	    id: string;
	    filename: string;
	    sizeBytes: number;
	    sha256: string;
	
	    static createFrom(source: any = {}) {
	        return new MarketAsset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.filename = source["filename"];
	        this.sizeBytes = source["sizeBytes"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class MarketItem {
	    id: string;
	    name: string;
	    description: string;
	    icon: string;
	    author: string;
	    version: string;
	    notes: string;
	    publishedAt: string;
	    asset?: MarketAsset;
	    updateAvailable?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MarketItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.icon = source["icon"];
	        this.author = source["author"];
	        this.version = source["version"];
	        this.notes = source["notes"];
	        this.publishedAt = source["publishedAt"];
	        this.asset = this.convertValues(source["asset"], MarketAsset);
	        this.updateAvailable = source["updateAvailable"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace task {
	
	export class Task {
	    id: string;
	    kind: string;
	    fileName: string;
	    localPath?: string;
	    direction: string;
	    peer: string;
	    batchId?: string;
	    totalBytes: number;
	    transferredBytes: number;
	    speed: number;
	    status: string;
	    error?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.localPath = source["localPath"];
	        this.direction = source["direction"];
	        this.peer = source["peer"];
	        this.batchId = source["batchId"];
	        this.totalBytes = source["totalBytes"];
	        this.transferredBytes = source["transferredBytes"];
	        this.speed = source["speed"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

