export namespace main {
	
	export class Config {
	    local_folder: string;
	    remote_folder_id: string;
	    sync_interval_seconds: number;
	    autostart: boolean;
	    dry_run: boolean;
	    sync_mode: string;
	    safety_shield: boolean;
	    allow_remote_deletion: boolean;
	    max_delete_threshold: number;
	    realtime_watch: boolean;
	    concurrent_uploads: number;
	    custom_ignore_rules: string[];
	    notifications_enabled: boolean;
	    bandwidth_limit_kbps: number;
	    conflict_strategy: string;
	    quiet_hours_enabled: boolean;
	    quiet_hours_start: string;
	    quiet_hours_end: string;
	    excluded_folders: string[];
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.local_folder = source["local_folder"];
	        this.remote_folder_id = source["remote_folder_id"];
	        this.sync_interval_seconds = source["sync_interval_seconds"];
	        this.autostart = source["autostart"];
	        this.dry_run = source["dry_run"];
	        this.sync_mode = source["sync_mode"];
	        this.safety_shield = source["safety_shield"];
	        this.allow_remote_deletion = source["allow_remote_deletion"];
	        this.max_delete_threshold = source["max_delete_threshold"];
	        this.realtime_watch = source["realtime_watch"];
	        this.concurrent_uploads = source["concurrent_uploads"];
	        this.custom_ignore_rules = source["custom_ignore_rules"];
	        this.notifications_enabled = source["notifications_enabled"];
	        this.bandwidth_limit_kbps = source["bandwidth_limit_kbps"];
	        this.conflict_strategy = source["conflict_strategy"];
	        this.quiet_hours_enabled = source["quiet_hours_enabled"];
	        this.quiet_hours_start = source["quiet_hours_start"];
	        this.quiet_hours_end = source["quiet_hours_end"];
	        this.excluded_folders = source["excluded_folders"];
	    }
	}
	export class FileRevisionItem {
	    id: string;
	    modified_time: string;
	    modified_time_ago: string;
	    size: number;
	    size_formatted: string;
	    keep_forever: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileRevisionItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.modified_time = source["modified_time"];
	        this.modified_time_ago = source["modified_time_ago"];
	        this.size = source["size"];
	        this.size_formatted = source["size_formatted"];
	        this.keep_forever = source["keep_forever"];
	    }
	}
	export class FolderItem {
	    id: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new FolderItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class RecentFileItem {
	    id: number;
	    rel_path: string;
	    action: string;
	    size: number;
	    size_formatted: string;
	    timestamp: string;
	    time_ago: string;
	    drive_link: string;
	
	    static createFrom(source: any = {}) {
	        return new RecentFileItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.rel_path = source["rel_path"];
	        this.action = source["action"];
	        this.size = source["size"];
	        this.size_formatted = source["size_formatted"];
	        this.timestamp = source["timestamp"];
	        this.time_ago = source["time_ago"];
	        this.drive_link = source["drive_link"];
	    }
	}
	export class SearchResultItem {
	    rel_path: string;
	    file_name: string;
	    folder: string;
	    file_id: string;
	    size: number;
	    size_formatted: string;
	    drive_link: string;
	    mtime: string;
	    synced_at: string;
	    is_dir: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SearchResultItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rel_path = source["rel_path"];
	        this.file_name = source["file_name"];
	        this.folder = source["folder"];
	        this.file_id = source["file_id"];
	        this.size = source["size"];
	        this.size_formatted = source["size_formatted"];
	        this.drive_link = source["drive_link"];
	        this.mtime = source["mtime"];
	        this.synced_at = source["synced_at"];
	        this.is_dir = source["is_dir"];
	    }
	}
	export class ShareLinkResult {
	    url: string;
	    file_id: string;
	    is_public: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ShareLinkResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.file_id = source["file_id"];
	        this.is_public = source["is_public"];
	        this.message = source["message"];
	    }
	}
	export class StorageCategoryStat {
	    category: string;
	    count: number;
	    total_bytes: number;
	    size_formatted: string;
	    percentage: number;
	    color: string;
	
	    static createFrom(source: any = {}) {
	        return new StorageCategoryStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.count = source["count"];
	        this.total_bytes = source["total_bytes"];
	        this.size_formatted = source["size_formatted"];
	        this.percentage = source["percentage"];
	        this.color = source["color"];
	    }
	}
	export class StorageBreakdown {
	    total_files: number;
	    total_bytes: number;
	    size_formatted: string;
	    categories: StorageCategoryStat[];
	
	    static createFrom(source: any = {}) {
	        return new StorageBreakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total_files = source["total_files"];
	        this.total_bytes = source["total_bytes"];
	        this.size_formatted = source["size_formatted"];
	        this.categories = this.convertValues(source["categories"], StorageCategoryStat);
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
	
	export class StorageQuotaInfo {
	    user_email: string;
	    user_name: string;
	    user_photo: string;
	    limit_bytes: number;
	    usage_bytes: number;
	    usage_in_drive: number;
	    usage_in_trash: number;
	    usage_percentage: number;
	    limit_formatted: string;
	    usage_formatted: string;
	    is_unlimited: boolean;
	
	    static createFrom(source: any = {}) {
	        return new StorageQuotaInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_email = source["user_email"];
	        this.user_name = source["user_name"];
	        this.user_photo = source["user_photo"];
	        this.limit_bytes = source["limit_bytes"];
	        this.usage_bytes = source["usage_bytes"];
	        this.usage_in_drive = source["usage_in_drive"];
	        this.usage_in_trash = source["usage_in_trash"];
	        this.usage_percentage = source["usage_percentage"];
	        this.limit_formatted = source["limit_formatted"];
	        this.usage_formatted = source["usage_formatted"];
	        this.is_unlimited = source["is_unlimited"];
	    }
	}
	export class TrayManager {
	
	
	    static createFrom(source: any = {}) {
	        return new TrayManager(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

