export namespace main {
	
	export class BucketInfo {
	    name: string;
	    creation_date: string;
	
	    static createFrom(source: any = {}) {
	        return new BucketInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.creation_date = source["creation_date"];
	    }
	}
	export class BucketStats {
	    object_count: number;
	    total_size: number;
	    last_modified: string;
	    location: string;
	
	    static createFrom(source: any = {}) {
	        return new BucketStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.object_count = source["object_count"];
	        this.total_size = source["total_size"];
	        this.last_modified = source["last_modified"];
	        this.location = source["location"];
	    }
	}
	export class Config {
	    account_id: string;
	    access_key_id: string;
	    secret_access_key: string;
	    api_token?: string;
	    download_concurrency?: number;
	    inline_previews?: boolean;
	    view_mode?: string;
	    delete_enabled?: boolean;
	    preview_size_limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.account_id = source["account_id"];
	        this.access_key_id = source["access_key_id"];
	        this.secret_access_key = source["secret_access_key"];
	        this.api_token = source["api_token"];
	        this.download_concurrency = source["download_concurrency"];
	        this.inline_previews = source["inline_previews"];
	        this.view_mode = source["view_mode"];
	        this.delete_enabled = source["delete_enabled"];
	        this.preview_size_limit = source["preview_size_limit"];
	    }
	}
	export class ObjectInfo {
	    key: string;
	    size: number;
	    last_modified: string;
	    is_folder: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ObjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.size = source["size"];
	        this.last_modified = source["last_modified"];
	        this.is_folder = source["is_folder"];
	    }
	}
	export class ListResult {
	    objects: ObjectInfo[];
	    prefixes: string[];
	    is_truncated: boolean;
	    next_token: string;
	
	    static createFrom(source: any = {}) {
	        return new ListResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.objects = this.convertValues(source["objects"], ObjectInfo);
	        this.prefixes = source["prefixes"];
	        this.is_truncated = source["is_truncated"];
	        this.next_token = source["next_token"];
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
	
	export class PrefixEstimate {
	    object_count: number;
	    total_size: number;
	    keys: string[];
	
	    static createFrom(source: any = {}) {
	        return new PrefixEstimate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.object_count = source["object_count"];
	        this.total_size = source["total_size"];
	        this.keys = source["keys"];
	    }
	}
	export class PreviewPayload {
	    type: string;
	    mime_type: string;
	    content?: string;
	    data_url?: string;
	    size: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PreviewPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.mime_type = source["mime_type"];
	        this.content = source["content"];
	        this.data_url = source["data_url"];
	        this.size = source["size"];
	        this.truncated = source["truncated"];
	    }
	}

}

