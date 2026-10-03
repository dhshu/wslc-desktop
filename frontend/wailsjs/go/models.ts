export namespace domain {
	
	export class Container {
	    ID: string;
	    Names: string[];
	    Image: string;
	    ImageID: string;
	    Command: string;
	    CreatedAt: string;
	    RunningFor: string;
	    Status: string;
	    State: string;
	    Ports: string[];
	    Size: string;
	    Labels: string;
	    Networks: string[];
	    Mounts: string;
	
	    static createFrom(source: any = {}) {
	        return new Container(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Names = source["Names"];
	        this.Image = source["Image"];
	        this.ImageID = source["ImageID"];
	        this.Command = source["Command"];
	        this.CreatedAt = source["CreatedAt"];
	        this.RunningFor = source["RunningFor"];
	        this.Status = source["Status"];
	        this.State = source["State"];
	        this.Ports = source["Ports"];
	        this.Size = source["Size"];
	        this.Labels = source["Labels"];
	        this.Networks = source["Networks"];
	        this.Mounts = source["Mounts"];
	    }
	}
	export class ContainerStats {
	    ID: string;
	    Name: string;
	    CPUPerc: string;
	    MemUsage: string;
	    MemPerc: string;
	    NetIO: string;
	    BlockIO: string;
	    PIDs: number;
	
	    static createFrom(source: any = {}) {
	        return new ContainerStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.CPUPerc = source["CPUPerc"];
	        this.MemUsage = source["MemUsage"];
	        this.MemPerc = source["MemPerc"];
	        this.NetIO = source["NetIO"];
	        this.BlockIO = source["BlockIO"];
	        this.PIDs = source["PIDs"];
	    }
	}
	export class Image {
	    ID: string;
	    Repository: string;
	    Tag: string;
	    Digest: string;
	    CreatedAt: string;
	    CreatedSince: string;
	    Size: string;
	    Labels: string;
	
	    static createFrom(source: any = {}) {
	        return new Image(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Repository = source["Repository"];
	        this.Tag = source["Tag"];
	        this.Digest = source["Digest"];
	        this.CreatedAt = source["CreatedAt"];
	        this.CreatedSince = source["CreatedSince"];
	        this.Size = source["Size"];
	        this.Labels = source["Labels"];
	    }
	}
	export class Network {
	    ID: string;
	    Name: string;
	    Driver: string;
	    Scope: string;
	    IPv6: string;
	    Internal: string;
	    CreatedAt: string;
	    Labels: string;
	    Containers: string[];
	
	    static createFrom(source: any = {}) {
	        return new Network(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.Driver = source["Driver"];
	        this.Scope = source["Scope"];
	        this.IPv6 = source["IPv6"];
	        this.Internal = source["Internal"];
	        this.CreatedAt = source["CreatedAt"];
	        this.Labels = source["Labels"];
	        this.Containers = source["Containers"];
	    }
	}
	export class Session {
	    ID: number;
	    Name: string;
	    CreatorPid: number;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Name = source["Name"];
	        this.CreatorPid = source["CreatorPid"];
	    }
	}
	export class Volume {
	    Name: string;
	    Driver: string;
	    Mountpoint: string;
	    Scope: string;
	    CreatedAt: string;
	    Labels: string;
	    Size: string;
	
	    static createFrom(source: any = {}) {
	        return new Volume(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Driver = source["Driver"];
	        this.Mountpoint = source["Mountpoint"];
	        this.Scope = source["Scope"];
	        this.CreatedAt = source["CreatedAt"];
	        this.Labels = source["Labels"];
	        this.Size = source["Size"];
	    }
	}

}

export namespace service {
	
	export class PresetImage {
	    Label: string;
	    Ref: string;
	
	    static createFrom(source: any = {}) {
	        return new PresetImage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Label = source["Label"];
	        this.Ref = source["Ref"];
	    }
	}
	export class AppSettings {
	    SchemaVersion: number;
	    MirrorEnabled: boolean;
	    MirrorEndpoint: string;
	    CustomMirrors: string[];
	    PresetImages: PresetImage[];
	
	    static createFrom(source: any = {}) {
	        return new AppSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SchemaVersion = source["SchemaVersion"];
	        this.MirrorEnabled = source["MirrorEnabled"];
	        this.MirrorEndpoint = source["MirrorEndpoint"];
	        this.CustomMirrors = source["CustomMirrors"];
	        this.PresetImages = this.convertValues(source["PresetImages"], PresetImage);
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
	export class BuildOptions {
	    Context: string;
	    Dockerfile: string;
	    Tags: string[];
	    BuildArgs: string[];
	    Target: string;
	    NoCache: boolean;
	    Pull: boolean;
	    Labels: string[];
	    Progress: string;
	
	    static createFrom(source: any = {}) {
	        return new BuildOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Context = source["Context"];
	        this.Dockerfile = source["Dockerfile"];
	        this.Tags = source["Tags"];
	        this.BuildArgs = source["BuildArgs"];
	        this.Target = source["Target"];
	        this.NoCache = source["NoCache"];
	        this.Pull = source["Pull"];
	        this.Labels = source["Labels"];
	        this.Progress = source["Progress"];
	    }
	}
	export class ContainerFilter {
	    All: boolean;
	    Query: string;
	    State: string;
	    Limit: number;
	
	    static createFrom(source: any = {}) {
	        return new ContainerFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.All = source["All"];
	        this.Query = source["Query"];
	        this.State = source["State"];
	        this.Limit = source["Limit"];
	    }
	}
	export class EnvStatus {
	    Available: boolean;
	    WslcPath: string;
	    WslcVersion: string;
	    WSLVersion: string;
	    KernelVersion: string;
	    SettingsFile: string;
	    ServiceReady: boolean;
	    Sessions: domain.Session[];
	    Problems: string[];
	    PullTip: string;
	    ActiveMirror: string;
	    SettingsPath: string;
	    // Go type: time
	    CheckedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new EnvStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Available = source["Available"];
	        this.WslcPath = source["WslcPath"];
	        this.WslcVersion = source["WslcVersion"];
	        this.WSLVersion = source["WSLVersion"];
	        this.KernelVersion = source["KernelVersion"];
	        this.SettingsFile = source["SettingsFile"];
	        this.ServiceReady = source["ServiceReady"];
	        this.Sessions = this.convertValues(source["Sessions"], domain.Session);
	        this.Problems = source["Problems"];
	        this.PullTip = source["PullTip"];
	        this.ActiveMirror = source["ActiveMirror"];
	        this.SettingsPath = source["SettingsPath"];
	        this.CheckedAt = this.convertValues(source["CheckedAt"], null);
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
	export class ExecOptions {
	    TTY: boolean;
	    User: string;
	    WorkDir: string;
	    Env: string[];
	
	    static createFrom(source: any = {}) {
	        return new ExecOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.TTY = source["TTY"];
	        this.User = source["User"];
	        this.WorkDir = source["WorkDir"];
	        this.Env = source["Env"];
	    }
	}
	export class LogsOptions {
	    Follow: boolean;
	    Tail: number;
	    Timestamps: boolean;
	    Since: string;
	    Until: string;
	
	    static createFrom(source: any = {}) {
	        return new LogsOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Follow = source["Follow"];
	        this.Tail = source["Tail"];
	        this.Timestamps = source["Timestamps"];
	        this.Since = source["Since"];
	        this.Until = source["Until"];
	    }
	}
	export class MirrorProbe {
	    Endpoint: string;
	    TargetRef: string;
	    OK: boolean;
	    DurationMS: number;
	    Message: string;
	
	    static createFrom(source: any = {}) {
	        return new MirrorProbe(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Endpoint = source["Endpoint"];
	        this.TargetRef = source["TargetRef"];
	        this.OK = source["OK"];
	        this.DurationMS = source["DurationMS"];
	        this.Message = source["Message"];
	    }
	}
	
	export class PruneResult {
	    Stdout: string;
	
	    static createFrom(source: any = {}) {
	        return new PruneResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Stdout = source["Stdout"];
	    }
	}
	export class RunContainerOptions {
	    Image: string;
	    Name: string;
	    Command: string[];
	    Detach: boolean;
	    Remove: boolean;
	    TTY: boolean;
	    Env: string[];
	    Ports: string[];
	    Volumes: string[];
	    Network: string;
	    WorkDir: string;
	    User: string;
	    Hostname: string;
	    Memory: string;
	    CPUs: string;
	    Entrypoint: string;
	    Labels: string[];
	    Pull: string;
	
	    static createFrom(source: any = {}) {
	        return new RunContainerOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Image = source["Image"];
	        this.Name = source["Name"];
	        this.Command = source["Command"];
	        this.Detach = source["Detach"];
	        this.Remove = source["Remove"];
	        this.TTY = source["TTY"];
	        this.Env = source["Env"];
	        this.Ports = source["Ports"];
	        this.Volumes = source["Volumes"];
	        this.Network = source["Network"];
	        this.WorkDir = source["WorkDir"];
	        this.User = source["User"];
	        this.Hostname = source["Hostname"];
	        this.Memory = source["Memory"];
	        this.CPUs = source["CPUs"];
	        this.Entrypoint = source["Entrypoint"];
	        this.Labels = source["Labels"];
	        this.Pull = source["Pull"];
	    }
	}
	export class Task {
	    ID: string;
	    Kind: string;
	    Ref: string;
	    State: string;
	    Output: string;
	    Error: string;
	    // Go type: time
	    StartedAt: any;
	    // Go type: time
	    EndedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Kind = source["Kind"];
	        this.Ref = source["Ref"];
	        this.State = source["State"];
	        this.Output = source["Output"];
	        this.Error = source["Error"];
	        this.StartedAt = this.convertValues(source["StartedAt"], null);
	        this.EndedAt = this.convertValues(source["EndedAt"], null);
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

