export namespace app {
	
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
	export class ComposedHeader {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new ComposedHeader(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}
	export class ComposedRequest {
	    method: string;
	    url: string;
	    headers: ComposedHeader[];
	    body: string;
	    skipVerify: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ComposedRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.url = source["url"];
	        this.headers = this.convertValues(source["headers"], ComposedHeader);
	        this.body = source["body"];
	        this.skipVerify = source["skipVerify"];
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
	    Source: string;
	    Historical: boolean;
	    Tags: string[];
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
	        this.Source = source["Source"];
	        this.Historical = source["Historical"];
	        this.Tags = source["Tags"];
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
	    Source: string;
	    Historical: boolean;
	    Tags: string[];
	
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
	        this.Source = source["Source"];
	        this.Historical = source["Historical"];
	        this.Tags = source["Tags"];
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
	export class SettingsView {
	    listenAddr: string;
	    upstreamMode: string;
	    upstreamProxy: string;
	    maxFlows: number;
	    maxBodyMB: number;
	    showSysProxySwitch: boolean;
	    autoSysProxy: boolean;
	    bypassList: string[];
	    persist: settings.PersistConfig;
	    adb: settings.ADBConfig;
	    filterGroups: rules.FilterGroup[];
	    decryptRules: rules.DecryptRule[];
	    currentProject: settings.ProjectMeta;
	    rulesProject: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listenAddr = source["listenAddr"];
	        this.upstreamMode = source["upstreamMode"];
	        this.upstreamProxy = source["upstreamProxy"];
	        this.maxFlows = source["maxFlows"];
	        this.maxBodyMB = source["maxBodyMB"];
	        this.showSysProxySwitch = source["showSysProxySwitch"];
	        this.autoSysProxy = source["autoSysProxy"];
	        this.bypassList = source["bypassList"];
	        this.persist = this.convertValues(source["persist"], settings.PersistConfig);
	        this.adb = this.convertValues(source["adb"], settings.ADBConfig);
	        this.filterGroups = this.convertValues(source["filterGroups"], rules.FilterGroup);
	        this.decryptRules = this.convertValues(source["decryptRules"], rules.DecryptRule);
	        this.currentProject = this.convertValues(source["currentProject"], settings.ProjectMeta);
	        this.rulesProject = source["rulesProject"];
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
	export class TagInfo {
	    id: string;
	    name: string;
	    count: number;
	    createdAt: number;
	    lastUsedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new TagInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.count = source["count"];
	        this.createdAt = source["createdAt"];
	        this.lastUsedAt = source["lastUsedAt"];
	    }
	}
	export class TagResult {
	    tag?: TagInfo;
	    tagged: number;
	    archived: number;
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new TagResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tag = this.convertValues(source["tag"], TagInfo);
	        this.tagged = source["tagged"];
	        this.archived = source["archived"];
	        this.skipped = source["skipped"];
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

export namespace rules {
	
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

}

export namespace settings {
	
	export class ADBDevice {
	    name: string;
	    path: string;
	    serial?: string;
	    autoSet: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ADBDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.serial = source["serial"];
	        this.autoSet = source["autoSet"];
	    }
	}
	export class ADBConfig {
	    deviceProxyHost: string;
	    configs: ADBDevice[];
	
	    static createFrom(source: any = {}) {
	        return new ADBConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceProxyHost = source["deviceProxyHost"];
	        this.configs = this.convertValues(source["configs"], ADBDevice);
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
	
	export class PersistConfig {
	    enabled: boolean;
	    dbPath?: string;
	    retainDays: number;
	    maxMB: number;
	
	    static createFrom(source: any = {}) {
	        return new PersistConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.dbPath = source["dbPath"];
	        this.retainDays = source["retainDays"];
	        this.maxMB = source["maxMB"];
	    }
	}
	export class ProjectMeta {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}

}

