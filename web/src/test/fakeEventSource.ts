type Listener = (event: MessageEvent<string>) => void;

/** A stand-in for the browser's EventSource that tests can drive by hand. */
export class FakeEventSource {
  static instances: FakeEventSource[] = [];

  static reset() {
    FakeEventSource.instances = [];
  }

  static get latest(): FakeEventSource {
    const last = FakeEventSource.instances.at(-1);
    if (!last) throw new Error('no EventSource has been created');
    return last;
  }

  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  readonly url: string;
  private readonly listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  close() {
    this.closed = true;
  }

  open() {
    this.onopen?.(new Event('open'));
  }

  fail() {
    this.onerror?.(new Event('error'));
  }

  /** Delivers a server message; objects are sent as JSON, strings as-is. */
  emit(type: string, data: unknown) {
    const payload = typeof data === 'string' ? data : JSON.stringify(data);
    const event = new MessageEvent<string>(type, { data: payload });
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

/** Stable function (not recreated per render) to pass to useStream. */
export const createFakeSource = (url: string): EventSource => new FakeEventSource(url) as unknown as EventSource;
