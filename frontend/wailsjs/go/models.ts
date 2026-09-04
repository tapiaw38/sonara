export namespace main {

	export class AnalysisResult {
	    response: string;
	    conversationId: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.response = source["response"];
	        this.conversationId = source["conversationId"];
	    }
	}
	export class CaptureStatus {
	    path: string;
	    capturedAt: string;
	    queued: boolean;
	    agentReady: boolean;

	    static createFrom(source: any = {}) {
	        return new CaptureStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.capturedAt = source["capturedAt"];
	        this.queued = source["queued"];
	        this.agentReady = source["agentReady"];
	    }
	}
	export class ListeningState {
	    listening: boolean;
	    transcript: string;
	    status: string;
	    response: string;
	    sequence: number;

	    static createFrom(source: any = {}) {
	        return new ListeningState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listening = source["listening"];
	        this.transcript = source["transcript"];
	        this.status = source["status"];
	        this.response = source["response"];
	        this.sequence = source["sequence"];
	    }
	}

}
