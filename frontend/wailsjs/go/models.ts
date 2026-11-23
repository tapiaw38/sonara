export namespace app {
	
	export class AnalysisResult {
	    response: string;
	
	    static createFrom(source: any = {}) {
	        return new AnalysisResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.response = source["response"];
	    }
	}
	export class AzureOpenAIForm {
	    hasKey: boolean;
	    endpoint: string;
	    chatApiVersion: string;
	    transcribeApiVersion: string;
	    chatDeployment: string;
	    transcribeDeployment: string;
	    liveDeployment: string;
	
	    static createFrom(source: any = {}) {
	        return new AzureOpenAIForm(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasKey = source["hasKey"];
	        this.endpoint = source["endpoint"];
	        this.chatApiVersion = source["chatApiVersion"];
	        this.transcribeApiVersion = source["transcribeApiVersion"];
	        this.chatDeployment = source["chatDeployment"];
	        this.transcribeDeployment = source["transcribeDeployment"];
	        this.liveDeployment = source["liveDeployment"];
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
	    partial: boolean;
	    status: string;
	    response: string;
	    sequence: number;
	    manualSend: boolean;
	    pendingSeconds: number;
	    streaming: boolean;
	    language: string;
	    translation: string;
	    droppedSegments: number;
	    audioSource: string;
	    sessionId: string;
	    audioGuessing: boolean;
	    audioCandidates: number;
	    retractions: number;
	    liveMode: string;
	
	    static createFrom(source: any = {}) {
	        return new ListeningState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listening = source["listening"];
	        this.transcript = source["transcript"];
	        this.partial = source["partial"];
	        this.status = source["status"];
	        this.response = source["response"];
	        this.sequence = source["sequence"];
	        this.manualSend = source["manualSend"];
	        this.pendingSeconds = source["pendingSeconds"];
	        this.streaming = source["streaming"];
	        this.language = source["language"];
	        this.translation = source["translation"];
	        this.droppedSegments = source["droppedSegments"];
	        this.audioSource = source["audioSource"];
	        this.sessionId = source["sessionId"];
	        this.audioGuessing = source["audioGuessing"];
	        this.audioCandidates = source["audioCandidates"];
	        this.retractions = source["retractions"];
	        this.liveMode = source["liveMode"];
	    }
	}
	export class MeetingStatus {
	    state: string;
	    sessionId: string;
	    elapsedMs: number;
	    provider: string;
	    microphoneState: string;
	    outputState: string;
	    pending: number;
	    failed: number;
	    message: string;
	    diarizeEnabled: boolean;
	    diarizeNote: string;
	    diarizeSessionId: string;
	    meetSpeakers: string[];
	
	    static createFrom(source: any = {}) {
	        return new MeetingStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.sessionId = source["sessionId"];
	        this.elapsedMs = source["elapsedMs"];
	        this.provider = source["provider"];
	        this.microphoneState = source["microphoneState"];
	        this.outputState = source["outputState"];
	        this.pending = source["pending"];
	        this.failed = source["failed"];
	        this.message = source["message"];
	        this.diarizeEnabled = source["diarizeEnabled"];
	        this.diarizeNote = source["diarizeNote"];
	        this.diarizeSessionId = source["diarizeSessionId"];
	        this.meetSpeakers = source["meetSpeakers"];
	    }
	}
	export class MeetingView {
	    sessionId: string;
	    startedAt: string;
	    language: string;
	    segments: storage.TranscriptSegment[];
	    speakers: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new MeetingView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.startedAt = source["startedAt"];
	        this.language = source["language"];
	        this.segments = this.convertValues(source["segments"], storage.TranscriptSegment);
	        this.speakers = source["speakers"];
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
	export class PracticeTurn {
	    Text: string;
	    ReplyText: string;
	    Audio: number[];
	
	    static createFrom(source: any = {}) {
	        return new PracticeTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Text = source["Text"];
	        this.ReplyText = source["ReplyText"];
	        this.Audio = source["Audio"];
	    }
	}
	export class ProviderStatus {
	    openai: boolean;
	    deepseek: boolean;
	    azure: boolean;
	    azureTranscribe: boolean;
	    gemini: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.openai = source["openai"];
	        this.deepseek = source["deepseek"];
	        this.azure = source["azure"];
	        this.azureTranscribe = source["azureTranscribe"];
	        this.gemini = source["gemini"];
	    }
	}

}

export namespace pipewire {
	
	export class Source {
	    target: string;
	    label: string;
	    running: boolean;
	    meeting: boolean;
	    app: string;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new Source(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.label = source["label"];
	        this.running = source["running"];
	        this.meeting = source["meeting"];
	        this.app = source["app"];
	        this.kind = source["kind"];
	    }
	}

}

export namespace storage {
	
	export class CapabilityProviders {
	    text: string;
	    vision: string;
	    liveTranscribe: string;
	    meeting: string;
	    liveMode: string;
	
	    static createFrom(source: any = {}) {
	        return new CapabilityProviders(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.vision = source["vision"];
	        this.liveTranscribe = source["liveTranscribe"];
	        this.meeting = source["meeting"];
	        this.liveMode = source["liveMode"];
	    }
	}
	export class SessionViewMessage {
	    role: string;
	    text: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionViewMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.text = source["text"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class SessionSummary {
	    id: string;
	    startedAt: string;
	    mode: string;
	    language: string;
	    messages: number;
	    preview: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.startedAt = source["startedAt"];
	        this.mode = source["mode"];
	        this.language = source["language"];
	        this.messages = source["messages"];
	        this.preview = source["preview"];
	        this.title = source["title"];
	    }
	}
	export class OpenSessionResult {
	    sessions: SessionSummary[];
	    messages: SessionViewMessage[];
	
	    static createFrom(source: any = {}) {
	        return new OpenSessionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessions = this.convertValues(source["sessions"], SessionSummary);
	        this.messages = this.convertValues(source["messages"], SessionViewMessage);
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
	export class SessionProfile {
	    scenario: string;
	    company: string;
	    role: string;
	    objective: string;
	    notes: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scenario = source["scenario"];
	        this.company = source["company"];
	        this.role = source["role"];
	        this.objective = source["objective"];
	        this.notes = source["notes"];
	    }
	}
	
	
	export class TranscriptSegment {
	    id: number;
	    track: string;
	    startMs: number;
	    endMs: number;
	    text: string;
	    status: string;
	    speakerKey: string;
	
	    static createFrom(source: any = {}) {
	        return new TranscriptSegment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.track = source["track"];
	        this.startMs = source["startMs"];
	        this.endMs = source["endMs"];
	        this.text = source["text"];
	        this.status = source["status"];
	        this.speakerKey = source["speakerKey"];
	    }
	}
	export class UIPreferences {
	    responsePanelHeight: number;
	    transcriptPanelHeight: number;
	    responsePanelShare: number;
	
	    static createFrom(source: any = {}) {
	        return new UIPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.responsePanelHeight = source["responsePanelHeight"];
	        this.transcriptPanelHeight = source["transcriptPanelHeight"];
	        this.responsePanelShare = source["responsePanelShare"];
	    }
	}

}

