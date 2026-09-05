export namespace capture {
	
	export class CurlResult {
	    command: string;
	    bodyOmitted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CurlResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.bodyOmitted = source["bodyOmitted"];
	    }
	}
	export class PeerCert {
	    Subject: string;
	    Issuer: string;
	    DNSNames: string[];
	    // Go type: time
	    NotBefore: any;
	    // Go type: time
	    NotAfter: any;
	
	    static createFrom(source: any = {}) {
	        return new PeerCert(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Subject = source["Subject"];
	        this.Issuer = source["Issuer"];
	        this.DNSNames = source["DNSNames"];
	        this.NotBefore = this.convertValues(source["NotBefore"], null);
	        this.NotAfter = this.convertValues(source["NotAfter"], null);
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
	export class TLSInfo {
	    ClientVersion: string;
	    ServerVersion: string;
	    ServerName: string;
	    PeerCerts: PeerCert[];
	
	    static createFrom(source: any = {}) {
	        return new TLSInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ClientVersion = source["ClientVersion"];
	        this.ServerVersion = source["ServerVersion"];
	        this.ServerName = source["ServerName"];
	        this.PeerCerts = this.convertValues(source["PeerCerts"], PeerCert);
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

export namespace main {
	
	export class BodyPayload {
	    Encoding: string;
	    ContentType: string;
	    Truncated: boolean;
	    Raw: number[];
	    Body: number[];
	    DecodeErr: string;
	
	    static createFrom(source: any = {}) {
	        return new BodyPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Encoding = source["Encoding"];
	        this.ContentType = source["ContentType"];
	        this.Truncated = source["Truncated"];
	        this.Raw = source["Raw"];
	        this.Body = source["Body"];
	        this.DecodeErr = source["DecodeErr"];
	    }
	}
	export class DomainGroupImportResult {
	    id: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new DomainGroupImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.count = source["count"];
	    }
	}
	export class DomainGroupInfo {
	    id: string;
	    name: string;
	    category: string;
	    count: number;
	    custom: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DomainGroupInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.count = source["count"];
	        this.custom = source["custom"];
	    }
	}
	export class DomainIndexEntry {
	    id: string;
	    file: string;
	    name: string;
	    category: string;
	
	    static createFrom(source: any = {}) {
	        return new DomainIndexEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.file = source["file"];
	        this.name = source["name"];
	        this.category = source["category"];
	    }
	}
	export class FlowDetail {
	    ID: string;
	    State: string;
	    Scheme: string;
	    Method: string;
	    Host: string;
	    Path: string;
	    URL: string;
	    Status: number;
	    DurationMS: number;
	    BytesUp: number;
	    BytesDown: number;
	    ProcessName: string;
	    PID: number;
	    ClientAddr: string;
	    StartedAt: number;
	    Err: string;
	    Pinned: boolean;
	    ReqURL: string;
	    ReqProto: string;
	    ReqHeader: Record<string, Array<string>>;
	    ReqBodySize: number;
	    RespProto: string;
	    RespHeader: Record<string, Array<string>>;
	    RespBodySize: number;
	    ProcessPath: string;
	    ServerAddr: string;
	    TLS?: capture.TLSInfo;
	
	    static createFrom(source: any = {}) {
	        return new FlowDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.State = source["State"];
	        this.Scheme = source["Scheme"];
	        this.Method = source["Method"];
	        this.Host = source["Host"];
	        this.Path = source["Path"];
	        this.URL = source["URL"];
	        this.Status = source["Status"];
	        this.DurationMS = source["DurationMS"];
	        this.BytesUp = source["BytesUp"];
	        this.BytesDown = source["BytesDown"];
	        this.ProcessName = source["ProcessName"];
	        this.PID = source["PID"];
	        this.ClientAddr = source["ClientAddr"];
	        this.StartedAt = source["StartedAt"];
	        this.Err = source["Err"];
	        this.Pinned = source["Pinned"];
	        this.ReqURL = source["ReqURL"];
	        this.ReqProto = source["ReqProto"];
	        this.ReqHeader = source["ReqHeader"];
	        this.ReqBodySize = source["ReqBodySize"];
	        this.RespProto = source["RespProto"];
	        this.RespHeader = source["RespHeader"];
	        this.RespBodySize = source["RespBodySize"];
	        this.ProcessPath = source["ProcessPath"];
	        this.ServerAddr = source["ServerAddr"];
	        this.TLS = this.convertValues(source["TLS"], capture.TLSInfo);
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
	export class FlowMeta {
	    ID: string;
	    State: string;
	    Scheme: string;
	    Method: string;
	    Host: string;
	    Path: string;
	    URL: string;
	    Status: number;
	    DurationMS: number;
	    BytesUp: number;
	    BytesDown: number;
	    ProcessName: string;
	    PID: number;
	    ClientAddr: string;
	    StartedAt: number;
	    Err: string;
	    Pinned: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FlowMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.State = source["State"];
	        this.Scheme = source["Scheme"];
	        this.Method = source["Method"];
	        this.Host = source["Host"];
	        this.Path = source["Path"];
	        this.URL = source["URL"];
	        this.Status = source["Status"];
	        this.DurationMS = source["DurationMS"];
	        this.BytesUp = source["BytesUp"];
	        this.BytesDown = source["BytesDown"];
	        this.ProcessName = source["ProcessName"];
	        this.PID = source["PID"];
	        this.ClientAddr = source["ClientAddr"];
	        this.StartedAt = source["StartedAt"];
	        this.Err = source["Err"];
	        this.Pinned = source["Pinned"];
	    }
	}
	export class ImportRulesResult {
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ImportRulesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.warnings = source["warnings"];
	    }
	}
	export class IndexImportResult {
	    id: string;
	    count: number;
	    skipped?: boolean;
	    err?: string;
	
	    static createFrom(source: any = {}) {
	        return new IndexImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.count = source["count"];
	        this.skipped = source["skipped"];
	        this.err = source["err"];
	    }
	}
	export class ProxyStatus {
	    Running: boolean;
	    Addr: string;
	    Mode: string;
	    FlowCount: number;
	    StartError: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Running = source["Running"];
	        this.Addr = source["Addr"];
	        this.Mode = source["Mode"];
	        this.FlowCount = source["FlowCount"];
	        this.StartError = source["StartError"];
	    }
	}
	export class SaveSettingsResult {
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new SaveSettingsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.warnings = source["warnings"];
	    }
	}
	export class SystemProxyStatus {
	    state: string;
	    server: string;
	    override: string;
	
	    static createFrom(source: any = {}) {
	        return new SystemProxyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.server = source["server"];
	        this.override = source["override"];
	    }
	}
	export class URLImportProbe {
	    kind: string;
	    entries?: DomainIndexEntry[];
	
	    static createFrom(source: any = {}) {
	        return new URLImportProbe(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.entries = this.convertValues(source["entries"], DomainIndexEntry);
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

export namespace rules {
	
	export class CaptureRule {
	    action: string;
	    host: string;
	    urlRe: string;
	    method: string;
	
	    static createFrom(source: any = {}) {
	        return new CaptureRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.host = source["host"];
	        this.urlRe = source["urlRe"];
	        this.method = source["method"];
	    }
	}
	export class DecryptRule {
	    action: string;
	    host: string;
	
	    static createFrom(source: any = {}) {
	        return new DecryptRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.host = source["host"];
	    }
	}
	export class FilterGroup {
	    id: string;
	    name: string;
	    enabled: boolean;
	    mode: string;
	    hosts: string[];
	    paths: string[];
	    processes: string[];
	
	    static createFrom(source: any = {}) {
	        return new FilterGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.mode = source["mode"];
	        this.hosts = source["hosts"];
	        this.paths = source["paths"];
	        this.processes = source["processes"];
	    }
	}
	export class ProcessRule {
	    action: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new ProcessRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.name = source["name"];
	    }
	}

}

export namespace settings {
	
	export class Settings {
	    listenAddr: string;
	    upstreamMode: string;
	    upstreamProxy: string;
	    maxFlows: number;
	    maxBodyMB: number;
	    showSysProxySwitch: boolean;
	    bypassList: string[];
	    filterGroups: rules.FilterGroup[];
	    decryptRules: rules.DecryptRule[];
	    captureRules?: rules.CaptureRule[];
	    processRules?: rules.ProcessRule[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listenAddr = source["listenAddr"];
	        this.upstreamMode = source["upstreamMode"];
	        this.upstreamProxy = source["upstreamProxy"];
	        this.maxFlows = source["maxFlows"];
	        this.maxBodyMB = source["maxBodyMB"];
	        this.showSysProxySwitch = source["showSysProxySwitch"];
	        this.bypassList = source["bypassList"];
	        this.filterGroups = this.convertValues(source["filterGroups"], rules.FilterGroup);
	        this.decryptRules = this.convertValues(source["decryptRules"], rules.DecryptRule);
	        this.captureRules = this.convertValues(source["captureRules"], rules.CaptureRule);
	        this.processRules = this.convertValues(source["processRules"], rules.ProcessRule);
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

