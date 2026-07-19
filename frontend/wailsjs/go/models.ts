export namespace api {
	
	export class Status {
	    core: boolean;
	    discovery: boolean;
	    receiver: boolean;
	    webdav: boolean;
	    driveMapped: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.core = source["core"];
	        this.discovery = source["discovery"];
	        this.receiver = source["receiver"];
	        this.webdav = source["webdav"];
	        this.driveMapped = source["driveMapped"];
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

export namespace task {
	
	export class Task {
	    id: string;
	    fileName: string;
	    localPath?: string;
	    direction: string;
	    peer: string;
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
	        this.fileName = source["fileName"];
	        this.localPath = source["localPath"];
	        this.direction = source["direction"];
	        this.peer = source["peer"];
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

