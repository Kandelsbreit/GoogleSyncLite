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

