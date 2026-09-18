export namespace app {
	
	export class AIChatHandle {
	    handle: string;
	
	    static createFrom(source: any = {}) {
	        return new AIChatHandle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.handle = source["handle"];
	    }
	}
	export class AIConfigView {
	    hasApiKey: boolean;
	    apiKeyMasked: string;
	    keyMissingWarn: string;
	
	    static createFrom(source: any = {}) {
	        return new AIConfigView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasApiKey = source["hasApiKey"];
	        this.apiKeyMasked = source["apiKeyMasked"];
	        this.keyMissingWarn = source["keyMissingWarn"];
	    }
	}
	export class AIModelsResult {
	    models: string[];
	
	    static createFrom(source: any = {}) {
	        return new AIModelsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.models = source["models"];
	    }
	}
	export class AITestResult {
	    model: string;
	    latencyMs: number;
	    ok: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new AITestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.latencyMs = source["latencyMs"];
	        this.ok = source["ok"];
	        this.message = source["message"];
	    }
	}
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
	    UpstreamAddr: string;
	    StartedAt: number;
	    Err: string;
	    Pinned: boolean;
	    Source: string;
	    Historical: boolean;
	    Tags: string[];
	    AIIntent: string;
	    AIConfidence: string;
	    AINeedsBody: boolean;
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
	        this.UpstreamAddr = source["UpstreamAddr"];
	        this.StartedAt = source["StartedAt"];
	        this.Err = source["Err"];
	        this.Pinned = source["Pinned"];
	        this.Source = source["Source"];
	        this.Historical = source["Historical"];
	        this.Tags = source["Tags"];
	        this.AIIntent = source["AIIntent"];
	        this.AIConfidence = source["AIConfidence"];
	        this.AINeedsBody = source["AINeedsBody"];
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
	    UpstreamAddr: string;
	    StartedAt: number;
	    Err: string;
	    Pinned: boolean;
	    Source: string;
	    Historical: boolean;
	    Tags: string[];
	    AIIntent: string;
	    AIConfidence: string;
	    AINeedsBody: boolean;
	
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
	        this.UpstreamAddr = source["UpstreamAddr"];
	        this.StartedAt = source["StartedAt"];
	        this.Err = source["Err"];
	        this.Pinned = source["Pinned"];
	        this.Source = source["Source"];
	        this.Historical = source["Historical"];
	        this.Tags = source["Tags"];
	        this.AIIntent = source["AIIntent"];
	        this.AIConfidence = source["AIConfidence"];
	        this.AINeedsBody = source["AINeedsBody"];
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
	export class ProviderView {
	    id: string;
	    name: string;
	    vendor: string;
	    tradeNo: string;
	    protocol?: string;
	    fetchNum: number;
	    province?: string;
	    city?: string;
	    isp?: string;
	    filterUsed?: boolean;
	    authMode: string;
	    username?: string;
	    hasSecret: boolean;
	    hasPassword: boolean;
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.vendor = source["vendor"];
	        this.tradeNo = source["tradeNo"];
	        this.protocol = source["protocol"];
	        this.fetchNum = source["fetchNum"];
	        this.province = source["province"];
	        this.city = source["city"];
	        this.isp = source["isp"];
	        this.filterUsed = source["filterUsed"];
	        this.authMode = source["authMode"];
	        this.username = source["username"];
	        this.hasSecret = source["hasSecret"];
	        this.hasPassword = source["hasPassword"];
	        this.active = source["active"];
	    }
	}
	export class PoolProviderResult {
	    provider: ProviderView;
	    restarted: boolean;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PoolProviderResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = this.convertValues(source["provider"], ProviderView);
	        this.restarted = source["restarted"];
	        this.warnings = source["warnings"];
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
	export class PoolStatusView {
	    mode: string;
	    activeProvider: string;
	    onEmpty: string;
	    running: boolean;
	    status?: proxypool.PoolStatus;
	
	    static createFrom(source: any = {}) {
	        return new PoolStatusView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.activeProvider = source["activeProvider"];
	        this.onEmpty = source["onEmpty"];
	        this.running = source["running"];
	        this.status = this.convertValues(source["status"], proxypool.PoolStatus);
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
	export class PoolUseResult {
	    status: PoolStatusView;
	    restarted: boolean;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PoolUseResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], PoolStatusView);
	        this.restarted = source["restarted"];
	        this.warnings = source["warnings"];
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
	export class PoolWriteResult {
	    restarted: boolean;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PoolWriteResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.restarted = source["restarted"];
	        this.warnings = source["warnings"];
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
	export class ProxyTestResult {
	    addr: string;
	    region?: string;
	    expireAt?: number;
	    withAuth: boolean;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ProxyTestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.region = source["region"];
	        this.expireAt = source["expireAt"];
	        this.withAuth = source["withAuth"];
	        this.count = source["count"];
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
	    autoCheckUpdate: boolean;
	    bypassList: string[];
	    persist: settings.PersistConfig;
	    adb: settings.ADBConfig;
	    ai: settings.AISettings;
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
	        this.autoCheckUpdate = source["autoCheckUpdate"];
	        this.bypassList = source["bypassList"];
	        this.persist = this.convertValues(source["persist"], settings.PersistConfig);
	        this.adb = this.convertValues(source["adb"], settings.ADBConfig);
	        this.ai = this.convertValues(source["ai"], settings.AISettings);
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
	export class UpdateInfo {
	    hasUpdate: boolean;
	    current: string;
	    latest: string;
	    url: string;
	    notes: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasUpdate = source["hasUpdate"];
	        this.current = source["current"];
	        this.latest = source["latest"];
	        this.url = source["url"];
	        this.notes = source["notes"];
	    }
	}
	export class WailsAddIgnoreResult {
	    ignore: persist.ReviewIgnore;
	    added: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WailsAddIgnoreResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ignore = this.convertValues(source["ignore"], persist.ReviewIgnore);
	        this.added = source["added"];
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
	export class WailsFlowListResult {
	    flows: FlowMeta[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new WailsFlowListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.flows = this.convertValues(source["flows"], FlowMeta);
	        this.total = source["total"];
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
	export class WailsHistogramResult {
	    start: number;
	    end: number;
	    buckets: persist.HistBucket[];
	
	    static createFrom(source: any = {}) {
	        return new WailsHistogramResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.end = source["end"];
	        this.buckets = this.convertValues(source["buckets"], persist.HistBucket);
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
	export class WailsTagsOverview {
	    tags: TagInfo[];
	    total: number;
	    totalFlows: number;
	
	    static createFrom(source: any = {}) {
	        return new WailsTagsOverview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tags = this.convertValues(source["tags"], TagInfo);
	        this.total = source["total"];
	        this.totalFlows = source["totalFlows"];
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

export namespace ctlapi {
	
	export class AIChatOptions {
	    includeReqBody: boolean;
	    includeRespBody: boolean;
	    language: string;
	
	    static createFrom(source: any = {}) {
	        return new AIChatOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.includeReqBody = source["includeReqBody"];
	        this.includeRespBody = source["includeRespBody"];
	        this.language = source["language"];
	    }
	}
	export class AIChatRequest {
	    mode: string;
	    flowId: string;
	    ids: string[];
	    question: string;
	    options: AIChatOptions;
	
	    static createFrom(source: any = {}) {
	        return new AIChatRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.flowId = source["flowId"];
	        this.ids = source["ids"];
	        this.question = source["question"];
	        this.options = this.convertValues(source["options"], AIChatOptions);
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

export namespace persist {
	
	export class HistBucket {
	    t0: number;
	    t1: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new HistBucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t0 = source["t0"];
	        this.t1 = source["t1"];
	        this.count = source["count"];
	    }
	}
	export class ReviewIgnore {
	    kind: string;
	    value: string;
	    createdAt: number;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new ReviewIgnore(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.value = source["value"];
	        this.createdAt = source["createdAt"];
	        this.note = source["note"];
	    }
	}
	export class ReviewListOpts {
	    Q: string;
	    SortKey: string;
	    SortDir: string;
	    ShowIgnored: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ReviewListOpts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Q = source["Q"];
	        this.SortKey = source["SortKey"];
	        this.SortDir = source["SortDir"];
	        this.ShowIgnored = source["ShowIgnored"];
	    }
	}

}

export namespace proxypool {
	
	export class EndpointView {
	    addr: string;
	    region: string;
	
	    static createFrom(source: any = {}) {
	        return new EndpointView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addr = source["addr"];
	        this.region = source["region"];
	    }
	}
	export class PoolStatus {
	    running: boolean;
	    vendor: string;
	    providerName: string;
	    current?: EndpointView;
	    expireAt: number;
	    remainSec: number;
	    poolSize: number;
	    surplusTotal: number;
	    lastFetchAt: number;
	    lastError: string;
	    onEmpty: string;
	    fetching: boolean;
	    degraded: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PoolStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.vendor = source["vendor"];
	        this.providerName = source["providerName"];
	        this.current = this.convertValues(source["current"], EndpointView);
	        this.expireAt = source["expireAt"];
	        this.remainSec = source["remainSec"];
	        this.poolSize = source["poolSize"];
	        this.surplusTotal = source["surplusTotal"];
	        this.lastFetchAt = source["lastFetchAt"];
	        this.lastError = source["lastError"];
	        this.onEmpty = source["onEmpty"];
	        this.fetching = source["fetching"];
	        this.degraded = source["degraded"];
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
	export class VendorMeta {
	    id: string;
	    name: string;
	    supportUserPass: boolean;
	    supportWhitelist: boolean;
	    supportRegion: boolean;
	
	    static createFrom(source: any = {}) {
	        return new VendorMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.supportUserPass = source["supportUserPass"];
	        this.supportWhitelist = source["supportWhitelist"];
	        this.supportRegion = source["supportRegion"];
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
	
	export class AIModelEntry {
	    provider?: string;
	    model: string;
	    alias?: string;
	    baseUrl?: string;
	    apiKey?: string;
	    current?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AIModelEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.alias = source["alias"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.current = source["current"];
	    }
	}
	export class AIConfig {
	    enabled: boolean;
	    provider: string;
	    baseUrl: string;
	    apiKey: string;
	    model: string;
	    temperature: number;
	    timeoutSec: number;
	    maxFlows: number;
	    maxKb: number;
	    redact: boolean;
	    autoFillIntent: boolean;
	    entries?: AIModelEntry[];
	
	    static createFrom(source: any = {}) {
	        return new AIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.provider = source["provider"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.timeoutSec = source["timeoutSec"];
	        this.maxFlows = source["maxFlows"];
	        this.maxKb = source["maxKb"];
	        this.redact = source["redact"];
	        this.autoFillIntent = source["autoFillIntent"];
	        this.entries = this.convertValues(source["entries"], AIModelEntry);
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
	
	export class AISettings {
	    enabled: boolean;
	    provider: string;
	    baseUrl: string;
	    model: string;
	    temperature: number;
	    timeoutSec: number;
	    maxFlows: number;
	    maxKb: number;
	    redact: boolean;
	    autoFillIntent: boolean;
	    entries?: AIModelEntry[];
	
	    static createFrom(source: any = {}) {
	        return new AISettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.provider = source["provider"];
	        this.baseUrl = source["baseUrl"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.timeoutSec = source["timeoutSec"];
	        this.maxFlows = source["maxFlows"];
	        this.maxKb = source["maxKb"];
	        this.redact = source["redact"];
	        this.autoFillIntent = source["autoFillIntent"];
	        this.entries = this.convertValues(source["entries"], AIModelEntry);
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
	export class ProxyProviderConf {
	    id: string;
	    name: string;
	    vendor: string;
	    tradeNo: string;
	    secret: string;
	    protocol?: string;
	    fetchNum?: number;
	    province?: string;
	    city?: string;
	    isp?: string;
	    filterUsed?: boolean;
	    authMode?: string;
	    username?: string;
	    password?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxyProviderConf(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.vendor = source["vendor"];
	        this.tradeNo = source["tradeNo"];
	        this.secret = source["secret"];
	        this.protocol = source["protocol"];
	        this.fetchNum = source["fetchNum"];
	        this.province = source["province"];
	        this.city = source["city"];
	        this.isp = source["isp"];
	        this.filterUsed = source["filterUsed"];
	        this.authMode = source["authMode"];
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}

}

