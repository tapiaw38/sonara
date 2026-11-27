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
	    speechDeployment: string;
	
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
	        this.speechDeployment = source["speechDeployment"];
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
	    transcriptFinal: boolean;
	    partial: boolean;
	    status: string;
	    response: string;
	    responseTranslation: string;
	    responseLines: assistant.TranslationLine[];
	    sequence: number;
	    manualSend: boolean;
	    pendingSeconds: number;
	    streaming: boolean;
	    language: string;
	    translation: string;
	    translationLines: assistant.TranslationLine[];
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
	        this.transcriptFinal = source["transcriptFinal"];
	        this.partial = source["partial"];
	        this.status = source["status"];
	        this.response = source["response"];
	        this.responseTranslation = source["responseTranslation"];
	        this.responseLines = this.convertValues(source["responseLines"], assistant.TranslationLine);
	        this.sequence = source["sequence"];
	        this.manualSend = source["manualSend"];
	        this.pendingSeconds = source["pendingSeconds"];
	        this.streaming = source["streaming"];
	        this.language = source["language"];
	        this.translation = source["translation"];
	        this.translationLines = this.convertValues(source["translationLines"], assistant.TranslationLine);
	        this.droppedSegments = source["droppedSegments"];
	        this.audioSource = source["audioSource"];
	        this.sessionId = source["sessionId"];
	        this.audioGuessing = source["audioGuessing"];
	        this.audioCandidates = source["audioCandidates"];
	        this.retractions = source["retractions"];
	        this.liveMode = source["liveMode"];
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

export namespace assistant {

	export class TranslationLine {
	    original: string;
	    translation: string;

	    static createFrom(source: any = {}) {
	        return new TranslationLine(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.original = source["original"];
	        this.translation = source["translation"];
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
	    practiceVoice: string;
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
	        this.practiceVoice = source["practiceVoice"];
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
	export class ProviderProjection {
	    name: string;
	    cost: usage.Projection;
	    unpriced: boolean;

	    static createFrom(source: any = {}) {
	        return new ProviderProjection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.cost = this.convertValues(source["cost"], usage.Projection);
	        this.unpriced = source["unpriced"];
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
	    perspective: string;
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
	        this.perspective = source["perspective"];
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
	export class UsageTotals {
	    calls: number;
	    errors: number;
	    input: number;
	    output: number;
	    audioSeconds: number;
	    characters: number;
	    usd: number;
	    unpriced: number;

	    static createFrom(source: any = {}) {
	        return new UsageTotals(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.calls = source["calls"];
	        this.errors = source["errors"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.audioSeconds = source["audioSeconds"];
	        this.characters = source["characters"];
	        this.usd = source["usd"];
	        this.unpriced = source["unpriced"];
	    }
	}
	export class UsageGroup {
	    name: string;
	    totals: UsageTotals;

	    static createFrom(source: any = {}) {
	        return new UsageGroup(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.totals = this.convertValues(source["totals"], UsageTotals);
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
	export class UsagePeriod {
	    id: string;
	    totals: UsageTotals;
	    providers: UsageGroup[];
	    functions: UsageGroup[];
	    models: UsageGroup[];

	    static createFrom(source: any = {}) {
	        return new UsagePeriod(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.totals = this.convertValues(source["totals"], UsageTotals);
	        this.providers = this.convertValues(source["providers"], UsageGroup);
	        this.functions = this.convertValues(source["functions"], UsageGroup);
	        this.models = this.convertValues(source["models"], UsageGroup);
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
	export class UsagePrice {
	    provider: string;
	    model: string;
	    enabled: boolean;
	    input: number;
	    output: number;
	    audioInput: number;
	    audioOutput: number;
	    perMinute: number;
	    perMillionChars: number;
	    source: string;
	    durationBilling: boolean;

	    static createFrom(source: any = {}) {
	        return new UsagePrice(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.enabled = source["enabled"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.audioInput = source["audioInput"];
	        this.audioOutput = source["audioOutput"];
	        this.perMinute = source["perMinute"];
	        this.perMillionChars = source["perMillionChars"];
	        this.source = source["source"];
	        this.durationBilling = source["durationBilling"];
	    }
	}
	export class UsageProjection {
	    observedDays: number;
	    elapsedDays: number;
	    monthDays: number;
	    monthHasData: boolean;
	    total: usage.Projection;
	    providers: ProviderProjection[];

	    static createFrom(source: any = {}) {
	        return new UsageProjection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.observedDays = source["observedDays"];
	        this.elapsedDays = source["elapsedDays"];
	        this.monthDays = source["monthDays"];
	        this.monthHasData = source["monthHasData"];
	        this.total = this.convertValues(source["total"], usage.Projection);
	        this.providers = this.convertValues(source["providers"], ProviderProjection);
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
	export class UsageReport {
	    projection: UsageProjection;
	    periods: UsagePeriod[];
	    prices: UsagePrice[];
	    catalogUpdated: string;

	    static createFrom(source: any = {}) {
	        return new UsageReport(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projection = this.convertValues(source["projection"], UsageProjection);
	        this.periods = this.convertValues(source["periods"], UsagePeriod);
	        this.prices = this.convertValues(source["prices"], UsagePrice);
	        this.catalogUpdated = source["catalogUpdated"];
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

export namespace usage {

	export class Projection {
	    weekUSD: number;
	    thirtyDayUSD: number;
	    monthPaceUSD: number;

	    static createFrom(source: any = {}) {
	        return new Projection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.weekUSD = source["weekUSD"];
	        this.thirtyDayUSD = source["thirtyDayUSD"];
	        this.monthPaceUSD = source["monthPaceUSD"];
	    }
	}

}

