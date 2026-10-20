export namespace main {
	
	export class Programme {
	    title: string;
	    description: string;
	    start: number;
	    stop: number;
	
	    static createFrom(source: any = {}) {
	        return new Programme(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.description = source["description"];
	        this.start = source["start"];
	        this.stop = source["stop"];
	    }
	}
	export class Channel {
	    id: number;
	    name: string;
	    group: string;
	    favorite: boolean;
	    logo: boolean;
	    now?: Programme;
	
	    static createFrom(source: any = {}) {
	        return new Channel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.group = source["group"];
	        this.favorite = source["favorite"];
	        this.logo = source["logo"];
	        this.now = this.convertValues(source["now"], Programme);
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
	export class ChannelQuery {
	    playlistId: number;
	    allGroups: boolean;
	    group: string;
	    favoritesOnly: boolean;
	    search: string;
	
	    static createFrom(source: any = {}) {
	        return new ChannelQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playlistId = source["playlistId"];
	        this.allGroups = source["allGroups"];
	        this.group = source["group"];
	        this.favoritesOnly = source["favoritesOnly"];
	        this.search = source["search"];
	    }
	}
	export class Group {
	    name: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.count = source["count"];
	    }
	}
	export class Playlist {
	    id: number;
	    name: string;
	    sourceType: string;
	    source: string;
	    channelCount: number;
	    lastRefreshedAt: number;
	    guideSource: string;
	    guideCustom: boolean;
	    guideUpdatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Playlist(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.sourceType = source["sourceType"];
	        this.source = source["source"];
	        this.channelCount = source["channelCount"];
	        this.lastRefreshedAt = source["lastRefreshedAt"];
	        this.guideSource = source["guideSource"];
	        this.guideCustom = source["guideCustom"];
	        this.guideUpdatedAt = source["guideUpdatedAt"];
	    }
	}
	export class GuideResult {
	    playlist: Playlist;
	    channels: number;
	    programmes: number;
	
	    static createFrom(source: any = {}) {
	        return new GuideResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playlist = this.convertValues(source["playlist"], Playlist);
	        this.channels = source["channels"];
	        this.programmes = source["programmes"];
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
	export class ImportResult {
	    playlist?: Playlist;
	    imported: number;
	    skipped: number;
	    notModified: boolean;
	    tooSoonMinutes: number;
	
	    static createFrom(source: any = {}) {
	        return new ImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playlist = this.convertValues(source["playlist"], Playlist);
	        this.imported = source["imported"];
	        this.skipped = source["skipped"];
	        this.notModified = source["notModified"];
	        this.tooSoonMinutes = source["tooSoonMinutes"];
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
	
	
	export class Settings {
	    playerPath: string;
	    playerName: string;
	    playerOverride: boolean;
	    playerError: string;
	    userAgent: string;
	    defaultAgent: string;
	    logLevel: string;
	    dataDir: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.playerPath = source["playerPath"];
	        this.playerName = source["playerName"];
	        this.playerOverride = source["playerOverride"];
	        this.playerError = source["playerError"];
	        this.userAgent = source["userAgent"];
	        this.defaultAgent = source["defaultAgent"];
	        this.logLevel = source["logLevel"];
	        this.dataDir = source["dataDir"];
	        this.version = source["version"];
	    }
	}
	export class UpdateInfo {
	    latest: string;
	    available: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.latest = source["latest"];
	        this.available = source["available"];
	    }
	}

}

