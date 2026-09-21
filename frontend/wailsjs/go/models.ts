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
	export class TrayManager {
	
	
	    static createFrom(source: any = {}) {
	        return new TrayManager(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

