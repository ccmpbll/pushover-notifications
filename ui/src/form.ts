// The settings form, as plain logic: what the host holds becomes a form, a form
// becomes what the host is sent, and the rules the module holds the settings to
// are written here again so a mistake is found on the page and not as a refused
// start. Tests hold the defaults and the tags to module.yaml and template.go, so
// the two halves cannot drift apart unnoticed.

/** What the host returns for a credential it holds, and takes to mean "keep it". */
export const MARKER = 'xxxxx';

export const KINDS = [
  'started',
  'finished',
  'failed',
  'cancelled',
  'attention',
  'lost',
] as const;
export type Kind = (typeof KINDS)[number];
export type Image = 'none' | 'thumbnail' | 'camera';
export const IMAGES: readonly Image[] = ['none', 'thumbnail', 'camera'];

export interface Rule {
  on: boolean;
  priority: number;
  sound: string;
  title: string;
  body: string;
  image: Image;
}

export const MIN_PRIORITY = -2;
export const MAX_PRIORITY = 1;
export const MAX_TITLE = 250;
export const MAX_BODY = 1024;

const rule = (
  on: boolean,
  priority: number,
  title: string,
  body: string,
): Rule => ({ on, priority, sound: '', title, body, image: 'none' });

/** What each message says before anyone changes it: the module's own defaults. */
export const DEFAULT_RULES: Readonly<Record<Kind, Rule>> = {
  started: rule(true, -1, '{printer}: print started', '{file}'),
  finished: rule(
    true,
    0,
    '{printer}: print finished',
    '{file}\nPrinted for {time_printed}\n{filament} of filament\nUsed {energy}',
  ),
  failed: rule(
    true,
    1,
    '{printer}: print {outcome}',
    '{file}\nStopped at {progress}',
  ),
  cancelled: rule(
    false,
    0,
    '{printer}: print {outcome}',
    '{file}\nStopped at {progress}',
  ),
  attention: rule(
    true,
    1,
    '{printer} needs you: {dialog_title}',
    '{dialog_text}\n{file}',
  ),
  lost: rule(
    true,
    1,
    '{printer} went offline during a print',
    '{file}\nLast seen at {progress}',
  ),
};

/** The tags each message type has a value for. */
export const TAGS_FOR: Readonly<Record<Kind, readonly string[]>> = {
  started: ['printer', 'file', 'started'],
  finished: [
    'printer',
    'file',
    'outcome',
    'time_printed',
    'filament',
    'energy',
    'cost',
    'started',
    'ended',
  ],
  failed: [
    'printer',
    'file',
    'outcome',
    'progress',
    'time_printed',
    'filament',
    'energy',
    'cost',
    'started',
    'ended',
  ],
  cancelled: [
    'printer',
    'file',
    'outcome',
    'progress',
    'time_printed',
    'filament',
    'energy',
    'cost',
    'started',
    'ended',
  ],
  attention: ['printer', 'file', 'progress', 'dialog_title', 'dialog_text'],
  lost: ['printer', 'file', 'progress'],
};

/** The tags that come from the Tasmota Plugs extension. */
export const NEEDS_TASMOTA: readonly string[] = ['energy', 'cost'];

/** What credentials the host already holds, so an untouched field is kept. */
export interface Saved {
  hasToken: boolean;
  hasUser: boolean;
  hasConnectToken: boolean;
}

export interface Form {
  /** Typed this time. Empty keeps what is stored. */
  token: string;
  user: string;
  connectToken: string;
  connectUrl: string;
  /** The zone {started} and {ended} are written in. Empty is the server's. */
  timezone: string;
  rules: Record<Kind, Rule>;
  /** Serials of printers nobody is told about. */
  muted: string[];
  /** The camera chosen for a printer, by serial. */
  cameras: Record<string, string>;
  saved: Saved;
}

const word = (
  config: Readonly<Record<string, unknown>>,
  key: string,
): string => {
  const value = config[key];

  // The host returns a setting in the type the manifest declares for it, or as
  // whatever an operator wrote by hand: a bool or a number is its text.
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'boolean' || typeof value === 'number')
    return String(value);

  return '';
};

/** "true" or "false" as a boolean, and anything else as the fallback. */
const flag = (text: string, fallback: boolean): boolean => {
  if (text === 'true') return true;
  if (text === 'false') return false;

  return fallback;
};

const isImage = (value: string): value is Image =>
  (IMAGES as readonly string[]).includes(value);

/**
 * The camera of each printer from the cameras setting, a JSON object of serial
 * to camera name. Text that is not that is no cameras, so a page opened on a
 * setting written some other way still opens.
 */
const camerasOf = (text: string): Record<string, string> => {
  const out: Record<string, string> = {};
  if (text === '') return out;

  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed))
      return out;
    for (const [serial, name] of Object.entries(parsed))
      if (typeof name === 'string' && serial !== '' && name !== '')
        out[serial] = name;
  } catch {
    // Not JSON: no cameras.
  }

  return out;
};

/** What the host holds, as a form. A key it cannot read stays at its default. */
export const fromConfig = (config: Readonly<Record<string, unknown>>): Form => {
  const rules = {} as Record<Kind, Rule>;

  for (const kind of KINDS) {
    const fallback = DEFAULT_RULES[kind];
    const notify = word(config, `notify_${kind}`);
    const priority = word(config, `priority_${kind}`);
    const image = word(config, `image_${kind}`);
    const title = word(config, `title_${kind}`);
    const body = word(config, `body_${kind}`);
    const level = Number(priority);

    rules[kind] = {
      on: flag(notify, fallback.on),
      priority:
        /^-?\d+$/.test(priority) &&
        level >= MIN_PRIORITY &&
        level <= MAX_PRIORITY
          ? level
          : fallback.priority,
      sound: word(config, `sound_${kind}`),
      title: title === '' ? fallback.title : title,
      body: body === '' ? fallback.body : body,
      image: isImage(image) ? image : fallback.image,
    };
  }

  const cameras = camerasOf(word(config, 'cameras'));

  return {
    token: '',
    user: '',
    connectToken: '',
    connectUrl: word(config, 'connect_url'),
    timezone: word(config, 'timezone'),
    rules,
    muted: word(config, 'muted_printers')
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s !== ''),
    cameras,
    saved: {
      hasToken: word(config, 'pushover_token') !== '',
      hasUser: word(config, 'pushover_user') !== '',
      hasConnectToken: word(config, 'connect_token') !== '',
    },
  };
};

/**
 * The value to write for a credential, or undefined to write nothing: a value
 * typed this time, the marker when one is already stored, and no key at all
 * otherwise. An empty string written for a credential is stored as one, and the
 * host then reads it back as the marker, which would make "none" look "saved".
 */
const keep = (typed: string, stored: boolean): string | undefined => {
  if (typed.trim() !== '') return typed.trim();

  return stored ? MARKER : undefined;
};

/**
 * What the host is sent. Every key is written, so one that was cleared is
 * cleared, except a credential that has no value (see keep). A credential that
 * was not touched goes back as the marker, which the host takes to mean "keep
 * what is stored", and is left out when none is stored, because the host
 * refuses the marker for a credential it does not hold.
 */
export const toSettings = (
  form: Form,
  saved: Saved,
): Record<string, string> => {
  const out: Record<string, string> = {
    connect_url: form.connectUrl.trim().replace(/\/+$/, ''),
    timezone: canonicalZone(form.timezone),
    muted_printers: [...form.muted].join(','),
  };

  const credentials: [string, string | undefined][] = [
    ['pushover_token', keep(form.token, saved.hasToken)],
    ['pushover_user', keep(form.user, saved.hasUser)],
    ['connect_token', keep(form.connectToken, saved.hasConnectToken)],
  ];
  for (const [key, value] of credentials) {
    if (value !== undefined) out[key] = value;
  }

  for (const kind of KINDS) {
    const r = form.rules[kind];
    out[`notify_${kind}`] = String(r.on);
    out[`priority_${kind}`] = String(r.priority);
    out[`sound_${kind}`] = r.sound;
    out[`title_${kind}`] = r.title;
    out[`body_${kind}`] = r.body;
    out[`image_${kind}`] = r.image;
  }

  // One setting holds every printer's camera, in serial order so the same
  // choice is always the same text.
  const serials = Object.keys(form.cameras).sort();
  out.cameras =
    serials.length === 0
      ? ''
      : JSON.stringify(
          Object.fromEntries(serials.map((s) => [s, form.cameras[s] ?? ''])),
        );

  return out;
};

/** The ports the manifest lets the module connect to for the Connect address. */
export const CONNECT_PORTS: readonly number[] = [80, 443, 8080, 8443];

const zoneName = (zone: string): string | undefined => {
  try {
    return new Intl.DateTimeFormat('en', {
      timeZone: zone.trim(),
    }).resolvedOptions().timeZone;
  } catch {
    return undefined;
  }
};

/**
 * A zone as the module reads it. The browser accepts a name in any case and the
 * module does not, so a name that is valid is written the way the browser spells
 * it. A name that is not valid is written as typed, and problems() says so.
 */
const canonicalZone = (zone: string): string =>
  zone.trim() === '' ? '' : (zoneName(zone) ?? zone.trim());

/**
 * What the host holds as a form, and the same again as the original to compare
 * against. Until a time zone is saved, the form shows the browser's. The
 * original keeps what is saved (nothing), so the page counts it as a change and
 * Save turns on: otherwise a person set up before this setting existed sees a
 * zone the module is not using and cannot save it.
 */
export const readForm = (
  config: Readonly<Record<string, unknown>>,
  zone: string,
): { form: Form; original: Form } => {
  const original = fromConfig(config);
  const form = structuredClone(original);
  if (form.timezone === '') form.timezone = zone;

  return { form, original };
};

/** The time zone the browser is in, which a person's own messages should use. */
export const browserZone = (): string => zoneName('') ?? '';

const TAG = /\{[a-z_]+\}/g;

/** What is wrong with a template for a message type, or undefined. */
export const templateProblem = (
  kind: Kind,
  text: string,
  field?: 'title' | 'body',
): string | undefined => {
  if (field === 'title' && text.trim() === '')
    return 'The title cannot be empty.';
  if (field === 'title' && Array.from(text).length > MAX_TITLE)
    return `Up to ${String(MAX_TITLE)} characters.`;
  if (field === 'body' && Array.from(text).length > MAX_BODY)
    return `Up to ${String(MAX_BODY)} characters.`;

  const allowed = TAGS_FOR[kind];
  for (const tag of text.match(TAG) ?? []) {
    const name = tag.slice(1, -1);
    if (!allowed.includes(name))
      return `${tag} is not a tag of this message. Use the tags listed below.`;
  }

  if (/[{}]/.test(text.replace(TAG, '')))
    return 'A { or } is not part of a tag. A tag is a lowercase name in braces, such as {file}.';

  return undefined;
};

/** A template filled in. A line with a tag that has no value is left out. */
export const fill = (
  text: string,
  values: Readonly<Record<string, string | undefined>>,
): string => {
  const lines: string[] = [];

  for (const line of text.replace(/\r\n/g, '\n').split('\n')) {
    const missing: string[] = [];
    const filled = line
      .replace(TAG, (tag) => {
        const value = values[tag.slice(1, -1)];
        if (value === undefined || value === '') {
          missing.push(tag);

          return '';
        }

        return value;
      })
      .trim();
    if (filled !== '' && missing.length === 0) lines.push(filled);
  }

  return lines.join('\n');
};

const SAMPLE: Readonly<Record<string, string>> = {
  printer: 'Core One',
  file: 'benchy.bgcode',
  progress: '63%',
  time_printed: '1h 23m',
  filament: '4.2 m',
  energy: '0.42 kWh',
  cost: '$0.06',
  started: '10:02',
  ended: '11:25',
  dialog_title: 'Filament runout',
  dialog_text: 'Load filament and press Continue.',
};

const OUTCOME: Readonly<Record<Kind, string>> = {
  started: '',
  finished: 'finished',
  failed: 'failed',
  cancelled: 'cancelled',
  attention: '',
  lost: '',
};

/** What a message would look like, with made-up values. */
export const preview = (
  kind: Kind,
  r: Rule,
  tasmota: boolean,
): { title: string; body: string } => {
  const values: Record<string, string> = { ...SAMPLE, outcome: OUTCOME[kind] };
  if (!tasmota) {
    delete values.energy;
    delete values.cost;
  }

  return { title: fill(r.title, values), body: fill(r.body, values) };
};

const CREDENTIAL = /^[A-Za-z0-9]{1,64}$/;

const addressProblem = (text: string): string | undefined => {
  if (text.trim() === '') return undefined;
  try {
    const url = new URL(text.trim());
    const bad =
      (url.protocol !== 'http:' && url.protocol !== 'https:') ||
      url.hostname === '' ||
      url.username !== '' ||
      url.password !== '' ||
      url.search !== '' ||
      url.hash !== '';
    if (!bad) {
      const port = Number(url.port || (url.protocol === 'https:' ? 443 : 80));
      if (CONNECT_PORTS.includes(port)) return undefined;

      return `This extension may only connect to port ${CONNECT_PORTS.join(', ')}.`;
    }
  } catch {
    // A text that is not an address at all is the same answer.
  }

  return 'An address such as http://127.0.0.1:8080, with no password or query.';
};

/** Every problem in the form, by field. Empty means it can be saved. */
export const problems = (form: Form, saved: Saved): Record<string, string> => {
  const found: Record<string, string> = {};

  if (form.token !== '' && !CREDENTIAL.test(form.token.trim()))
    found.token = 'Letters and digits only, up to 64.';
  if (form.user !== '' && !CREDENTIAL.test(form.user.trim()))
    found.user = 'Letters and digits only, up to 64.';

  const hasToken = saved.hasToken || form.token.trim() !== '';
  const hasUser = saved.hasUser || form.user.trim() !== '';
  if (hasToken !== hasUser)
    found.pair = 'Set both the token and the user key, or neither.';

  const url = addressProblem(form.connectUrl);
  if (url) found.connectUrl = url;
  if (form.timezone.trim() !== '' && zoneName(form.timezone) === undefined)
    found.timezone =
      'A time zone name such as Europe/Berlin or America/New_York.';
  if (/\s/.test(form.connectToken.trim()))
    found.connectToken = 'No spaces in a token.';

  const wantsPicture = KINDS.some(
    (k) => form.rules[k].on && form.rules[k].image !== 'none',
  );
  const hasConnectToken =
    saved.hasConnectToken || form.connectToken.trim() !== '';
  if (wantsPicture && (form.connectUrl.trim() === '' || !hasConnectToken))
    found.connect =
      'A picture needs the address of Prusa Connect and a read-only API token.';

  for (const kind of KINDS) {
    const title = templateProblem(kind, form.rules[kind].title, 'title');
    if (title) found[`title.${kind}`] = title;
    const body = templateProblem(kind, form.rules[kind].body, 'body');
    if (body) found[`body.${kind}`] = body;
  }

  return found;
};

const normal = (form: Form): string =>
  JSON.stringify({
    token: form.token,
    user: form.user,
    connectToken: form.connectToken,
    connectUrl: form.connectUrl.trim(),
    timezone: canonicalZone(form.timezone),
    rules: KINDS.map((k) => form.rules[k]),
    muted: [...form.muted].sort(),
    cameras: Object.keys(form.cameras)
      .sort()
      .map((s) => [s, form.cameras[s]]),
  });

/** Whether the form differs from the one it was read as. */
export const dirty = (form: Form, original: Form): boolean =>
  normal(form) !== normal(original);

/** What a message type's test did, as the module answers it. */
export interface KindTestAnswer {
  printer: string;
  picture: string;
  picture_problem: string;
}

export type KindTestOutcome =
  | { what: 'no_picture'; reason: string }
  | { what: 'picture'; printer: string; picture: string }
  | { what: 'plain'; printer: string };

/**
 * What to tell the person about a test. A picture that was asked for and not
 * got comes first: the text went out, and the reason is the point.
 */
export const kindTestOutcome = (answer: KindTestAnswer): KindTestOutcome => {
  if (answer.picture_problem !== '')
    return { what: 'no_picture', reason: answer.picture_problem };
  if (answer.picture !== 'none')
    return {
      what: 'picture',
      printer: answer.printer,
      picture: answer.picture,
    };

  return { what: 'plain', printer: answer.printer };
};
