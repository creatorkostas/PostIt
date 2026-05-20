export namespace api {
	
	export class FuzzResult {
	    field: string;
	    payload: string;
	    statusCode: number;
	    responseTime: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new FuzzResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.field = source["field"];
	        this.payload = source["payload"];
	        this.statusCode = source["statusCode"];
	        this.responseTime = source["responseTime"];
	        this.error = source["error"];
	    }
	}
	export class HammerResults {
	    TotalRequests: number;
	    SuccessCount: number;
	    FailureCount: number;
	    TotalDuration: number;
	    AverageLatency: number;
	    P95Latency: number;
	    P99Latency: number;
	    RPS: number;
	    StatusCodes: Record<number, number>;
	    Latencies: number[];
	    // Go type: sync
	    Mutex: any;
	
	    static createFrom(source: any = {}) {
	        return new HammerResults(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TotalRequests = source["TotalRequests"];
	        this.SuccessCount = source["SuccessCount"];
	        this.FailureCount = source["FailureCount"];
	        this.TotalDuration = source["TotalDuration"];
	        this.AverageLatency = source["AverageLatency"];
	        this.P95Latency = source["P95Latency"];
	        this.P99Latency = source["P99Latency"];
	        this.RPS = source["RPS"];
	        this.StatusCodes = source["StatusCodes"];
	        this.Latencies = source["Latencies"];
	        this.Mutex = this.convertValues(source["Mutex"], null);
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
	export class RunnerResult {
	    iteration: number;
	    statusCode: number;
	    statusText: string;
	    duration: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunnerResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.iteration = source["iteration"];
	        this.statusCode = source["statusCode"];
	        this.statusText = source["statusText"];
	        this.duration = source["duration"];
	        this.error = source["error"];
	    }
	}
	export class WSMessage {
	    type: string;
	    content: string;
	    timestamp: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new WSMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.content = source["content"];
	        this.timestamp = this.convertValues(source["timestamp"], time.Time);
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
	export class WorkflowLog {
	    nodeId: string;
	    statusCode: number;
	    statusText: string;
	    body: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowLog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodeId = source["nodeId"];
	        this.statusCode = source["statusCode"];
	        this.statusText = source["statusText"];
	        this.body = source["body"];
	        this.error = source["error"];
	    }
	}

}

export namespace models {
	
	export class BasicAuth {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new BasicAuth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}
	export class Header {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new Header(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}
	export class Auth {
	    type: string;
	    bearer?: Header[];
	    basic?: BasicAuth[];
	
	    static createFrom(source: any = {}) {
	        return new Auth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.bearer = this.convertValues(source["bearer"], Header);
	        this.basic = this.convertValues(source["basic"], BasicAuth);
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
	
	export class RawOptions {
	    language: string;
	
	    static createFrom(source: any = {}) {
	        return new RawOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.language = source["language"];
	    }
	}
	export class Options {
	    raw?: RawOptions;
	
	    static createFrom(source: any = {}) {
	        return new Options(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.raw = this.convertValues(source["raw"], RawOptions);
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
	export class UrlEncoded {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new UrlEncoded(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}
	export class Body {
	    mode: string;
	    raw?: string;
	    urlencoded?: UrlEncoded[];
	    options?: Options;
	
	    static createFrom(source: any = {}) {
	        return new Body(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.raw = source["raw"];
	        this.urlencoded = this.convertValues(source["urlencoded"], UrlEncoded);
	        this.options = this.convertValues(source["options"], Options);
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
	export class Environment {
	    id: string;
	    name: string;
	    variables: Record<string, string>;
	    secret_vars?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Environment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.variables = source["variables"];
	        this.secret_vars = source["secret_vars"];
	    }
	}
	export class Script {
	    exec: string[];
	    type: string;
	
	    static createFrom(source: any = {}) {
	        return new Script(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exec = source["exec"];
	        this.type = source["type"];
	    }
	}
	export class Event {
	    listen: string;
	    script: Script;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listen = source["listen"];
	        this.script = this.convertValues(source["script"], Script);
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
	export class Extract {
	    sourcePath: string;
	    targetVar: string;
	
	    static createFrom(source: any = {}) {
	        return new Extract(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourcePath = source["sourcePath"];
	        this.targetVar = source["targetVar"];
	    }
	}
	
	export class HistoryRecord {
	    timestamp: time.Time;
	    path: string;
	    method: string;
	    url: string;
	    statusCode: number;
	    statusText: string;
	    duration: number;
	    responseBody?: string;
	    responseHeaders?: Record<string, Array<string>>;
	
	    static createFrom(source: any = {}) {
	        return new HistoryRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = this.convertValues(source["timestamp"], time.Time);
	        this.path = source["path"];
	        this.method = source["method"];
	        this.url = source["url"];
	        this.statusCode = source["statusCode"];
	        this.statusText = source["statusText"];
	        this.duration = source["duration"];
	        this.responseBody = source["responseBody"];
	        this.responseHeaders = source["responseHeaders"];
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
	export class MockResponse {
	    name: string;
	    code: number;
	    status: string;
	    body: string;
	    header: Header[];
	    condition?: string;
	    delay?: number;
	
	    static createFrom(source: any = {}) {
	        return new MockResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.code = source["code"];
	        this.status = source["status"];
	        this.body = source["body"];
	        this.header = this.convertValues(source["header"], Header);
	        this.condition = source["condition"];
	        this.delay = source["delay"];
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
	
	
	export class URL {
	    raw: string;
	
	    static createFrom(source: any = {}) {
	        return new URL(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.raw = source["raw"];
	    }
	}
	export class Request {
	    method: string;
	    header: Header[];
	    body?: Body;
	    url: URL;
	    auth?: Auth;
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.header = this.convertValues(source["header"], Header);
	        this.body = this.convertValues(source["body"], Body);
	        this.url = this.convertValues(source["url"], URL);
	        this.auth = this.convertValues(source["auth"], Auth);
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
	export class RequestInfo {
	    path: string;
	    request?: Request;
	    responses?: MockResponse[];
	    events?: Event[];
	    order: number;
	    sql_query?: string;
	    db_path?: string;
	    sql_driver?: string;
	    sql_target_var?: string;
	    sql_target_col?: string;
	    schema?: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new RequestInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.request = this.convertValues(source["request"], Request);
	        this.responses = this.convertValues(source["responses"], MockResponse);
	        this.events = this.convertValues(source["events"], Event);
	        this.order = source["order"];
	        this.sql_query = source["sql_query"];
	        this.db_path = source["db_path"];
	        this.sql_driver = source["sql_driver"];
	        this.sql_target_var = source["sql_target_var"];
	        this.sql_target_col = source["sql_target_col"];
	        this.schema = source["schema"];
	        this.note = source["note"];
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
	
	
	
	export class WorkflowEdge {
	    fromNode: string;
	    toNode: string;
	    type?: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowEdge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fromNode = source["fromNode"];
	        this.toNode = source["toNode"];
	        this.type = source["type"];
	    }
	}
	export class WorkflowNode {
	    id: string;
	    type: string;
	    requestPath?: string;
	    waitTime?: number;
	    condition?: string;
	    loopPath?: string;
	    maxIterations?: number;
	    script?: string;
	    variableName?: string;
	    x: number;
	    y: number;
	    extracts?: Extract[];
	
	    static createFrom(source: any = {}) {
	        return new WorkflowNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.requestPath = source["requestPath"];
	        this.waitTime = source["waitTime"];
	        this.condition = source["condition"];
	        this.loopPath = source["loopPath"];
	        this.maxIterations = source["maxIterations"];
	        this.script = source["script"];
	        this.variableName = source["variableName"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.extracts = this.convertValues(source["extracts"], Extract);
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
	export class Workflow {
	    id: string;
	    name: string;
	    nodes: WorkflowNode[];
	    edges: WorkflowEdge[];
	    status?: string;
	    waitingFor?: string;
	    currentNode?: string;
	
	    static createFrom(source: any = {}) {
	        return new Workflow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.nodes = this.convertValues(source["nodes"], WorkflowNode);
	        this.edges = this.convertValues(source["edges"], WorkflowEdge);
	        this.status = source["status"];
	        this.waitingFor = source["waitingFor"];
	        this.currentNode = source["currentNode"];
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

export namespace struct { Ciphertext string "json:\"ciphertext\"" } {
	
	export class  {
	    ciphertext: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ciphertext = source["ciphertext"];
	    }
	}

}

export namespace struct { Count int "json:\"count\"" } {
	
	export class  {
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.count = source["count"];
	    }
	}

}

export namespace struct { Driver string "json:\"driver\""; ConnStr string "json:\"connStr\""; Query string "json:\"query\"" } {
	
	export class  {
	    driver: string;
	    connStr: string;
	    query: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.driver = source["driver"];
	        this.connStr = source["connStr"];
	        this.query = source["query"];
	    }
	}

}

export namespace struct { ID string "json:\"id\"" } {
	
	export class  {
	    id: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	    }
	}

}

export namespace struct { JSON string "json:\"json\"" } {
	
	export class  {
	    json: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.json = source["json"];
	    }
	}

}

export namespace struct { Message string "json:\"message\"" } {
	
	export class  {
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = source["message"];
	    }
	}

}

export namespace struct { OldPath string "json:\"oldPath\""; NewPath string "json:\"newPath\"" } {
	
	export class  {
	    oldPath: string;
	    newPath: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.oldPath = source["oldPath"];
	        this.newPath = source["newPath"];
	    }
	}

}

export namespace struct { Password string "json:\"password\"" } {
	
	export class  {
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.password = source["password"];
	    }
	}

}

export namespace struct { Path string "json:\"path\"" } {
	
	export class  {
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	    }
	}

}

export namespace struct { Path string "json:\"path\""; Data map[string]string "json:\"data\"" } {
	
	export class  {
	    path: string;
	    data: any[];
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.data = source["data"];
	    }
	}

}

export namespace struct { Path string "json:\"path\""; MockName string "json:\"mockName\"" } {
	
	export class  {
	    path: string;
	    mockName: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.mockName = source["mockName"];
	    }
	}

}

export namespace struct { Path string "json:\"path\""; NewPath string "json:\"newPath\"" } {
	
	export class  {
	    path: string;
	    newPath: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.newPath = source["newPath"];
	    }
	}

}

export namespace struct { Path string "json:\"path\""; Schema string "json:\"schema\"" } {
	
	export class  {
	    path: string;
	    schema: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.schema = source["schema"];
	    }
	}

}

export namespace struct { Path string "json:\"path\""; Workers int "json:\"workers\""; Seconds int "json:\"seconds\"" } {
	
	export class  {
	    path: string;
	    workers: number;
	    seconds: number;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.workers = source["workers"];
	        this.seconds = source["seconds"];
	    }
	}

}

export namespace struct { Paths string "json:\"paths\"" } {
	
	export class  {
	    paths: string[];
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	    }
	}

}

export namespace struct { Plaintext string "json:\"plaintext\"" } {
	
	export class  {
	    plaintext: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.plaintext = source["plaintext"];
	    }
	}

}

export namespace struct { Port int "json:\"port\"" } {
	
	export class  {
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.port = source["port"];
	    }
	}

}

export namespace struct { Running bool "json:\"running\"" } {
	
	export class  {
	    running: boolean;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	    }
	}

}

export namespace struct { URL string "json:\"url\"" } {
	
	export class  {
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new (source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	    }
	}

}

export namespace time {
	
	export class Time {
	
	
	    static createFrom(source: any = {}) {
	        return new Time(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

