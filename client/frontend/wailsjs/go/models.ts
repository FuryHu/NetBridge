export namespace main {
	
	export class PeerView {
	    id: string;
	    nickName: string;
	    vip: string;
	    publicAddr: string;
	    v4?: string;
	    v6?: string;
	    channel: string;
	    voiceOn?: boolean;
	    micOn?: boolean;
	    isIPv6: boolean;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new PeerView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.nickName = source["nickName"];
	        this.vip = source["vip"];
	        this.publicAddr = source["publicAddr"];
	        this.v4 = source["v4"];
	        this.v6 = source["v6"];
	        this.channel = source["channel"];
	        this.voiceOn = source["voiceOn"];
	        this.micOn = source["micOn"];
	        this.isIPv6 = source["isIPv6"];
	        this.latency = source["latency"];
	    }
	}

}

