import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import {
  DEFAULT_RULES,
  CONNECT_PORTS,
  KINDS,
  MARKER,
  TAGS_FOR,
  dirty,
  fill,
  fromConfig,
  preview,
  problems,
  readForm,
  kindTestOutcome,
  templateProblem,
  toSettings,
  type Form,
  type Saved,
} from './form';

const repo = (name: string): string =>
  readFileSync(new URL(`../../${name}`, import.meta.url), 'utf8');

// The defaults the manifest declares, read the way the host reads them.
const manifestDefaults = (): Map<string, string> => {
  const found = new Map<string, string>();
  const pattern =
    /- key: (\S+)\n {4}type: \S+\n {4}default: ("(?:[^"\\]|\\.)*")/g;
  for (const match of repo('module.yaml').matchAll(pattern)) {
    found.set(match[1] ?? '', JSON.parse(match[2] ?? '""') as string);
  }

  return found;
};

const saved = (over: Partial<Saved> = {}): Saved => ({
  hasToken: true,
  hasUser: true,
  hasConnectToken: false,
  ...over,
});

const base = (): Form => fromConfig({});

describe('the defaults', () => {
  it('are the defaults the manifest declares', () => {
    const declared = manifestDefaults();
    for (const kind of KINDS) {
      const rule = DEFAULT_RULES[kind];
      expect(declared.get(`notify_${kind}`), kind).toBe(String(rule.on));
      expect(declared.get(`priority_${kind}`), kind).toBe(
        String(rule.priority),
      );
      expect(declared.get(`title_${kind}`), kind).toBe(rule.title);
      expect(declared.get(`body_${kind}`), kind).toBe(rule.body);
      expect(declared.get(`image_${kind}`), kind).toBe(rule.image);
      expect(declared.get(`sound_${kind}`), kind).toBe(rule.sound);
    }
  });

  it('offer, for each message type, the tags the module accepts', () => {
    const source = repo('template.go');
    const block =
      /var tagsFor = map\[string\]\[\]string\{([\s\S]*?)\n\}/.exec(
        source,
      )?.[1] ?? '';
    const inGo = new Map<string, string[]>();
    for (const line of block.matchAll(/"(\w+)":\s+\{([^}]*)\}/g)) {
      inGo.set(
        line[1] ?? '',
        [...(line[2] ?? '').matchAll(/"(\w+)"/g)].map((m) => m[1] ?? ''),
      );
    }

    expect(inGo.size).toBe(KINDS.length);
    for (const kind of KINDS) {
      expect([...TAGS_FOR[kind]], kind).toEqual(inGo.get(kind));
    }
  });
});

describe('reading what the host holds', () => {
  it('falls back to the defaults for a key that is not there', () => {
    const form = base();
    expect(form.rules.finished).toEqual(DEFAULT_RULES.finished);
    expect(form.muted).toEqual([]);
    expect(form.cameras).toEqual({});
  });

  it('reads the keys the host sends', () => {
    const form = fromConfig({
      notify_started: 'false',
      priority_failed: '-2',
      sound_finished: 'magic',
      title_attention: 'T {printer}',
      body_lost: 'B',
      image_finished: 'camera',
      muted_printers: ' A , B ,,',
      connect_url: 'http://127.0.0.1:8080',
      cameras: JSON.stringify({ 'SER-A': 'bench-top', 'SER-B': 'mk4s' }),
    });

    expect(form.rules.started.on).toBe(false);
    expect(form.rules.failed.priority).toBe(-2);
    expect(form.rules.finished.sound).toBe('magic');
    expect(form.rules.finished.image).toBe('camera');
    expect(form.rules.attention.title).toBe('T {printer}');
    expect(form.rules.lost.body).toBe('B');
    expect(form.muted).toEqual(['A', 'B']);
    expect(form.connectUrl).toBe('http://127.0.0.1:8080');
    expect(form.cameras).toEqual({ 'SER-A': 'bench-top', 'SER-B': 'mk4s' });
  });

  it('keeps a value it cannot read at its default instead of failing', () => {
    const form = fromConfig({ priority_failed: 'high', image_started: 'gif' });
    expect(form.rules.failed.priority).toBe(DEFAULT_RULES.failed.priority);
    expect(form.rules.started.image).toBe('none');
  });

  it('knows which credentials are stored, by the marker the host returns', () => {
    const config = { pushover_token: MARKER, pushover_user: '' };
    expect(fromConfig(config).saved).toEqual({
      hasToken: true,
      hasUser: false,
      hasConnectToken: false,
    });
  });
});

describe('writing what the host should hold', () => {
  it('writes every key, so a key that was cleared is cleared', () => {
    const out = toSettings(base(), saved({ hasConnectToken: true }));
    for (const key of manifestDefaults().keys()) {
      expect(out, key).toHaveProperty(key);
    }
  });

  it('sends the marker for a credential that was not touched, and the value for one that was', () => {
    const untouched = toSettings(base(), saved());
    expect(untouched.pushover_token).toBe(MARKER);
    expect(untouched.pushover_user).toBe(MARKER);

    const typed = { ...base(), token: 'abc123', user: 'u1' };
    const out = toSettings(typed, saved());
    expect(out.pushover_token).toBe('abc123');
    expect(out.pushover_user).toBe('u1');
  });

  it('writes no key at all for a credential that has no value', () => {
    // An empty string is stored as a credential and reads back as the marker,
    // and the marker is refused for one that has nothing to keep.
    const out = toSettings(base(), saved({ hasToken: false, hasUser: false }));
    expect(out).not.toHaveProperty('pushover_token');
    expect(out).not.toHaveProperty('pushover_user');
    expect(out).not.toHaveProperty('connect_token');
    expect(Object.values(out)).not.toContain(MARKER);
  });

  it('writes booleans and numbers as text, and every camera into one setting in serial order', () => {
    const form = base();
    form.rules.started.on = false;
    form.rules.failed.priority = -1;
    form.cameras = { 'SER-B': 'cam b', 'SER-A': 'cam a' };

    const out = toSettings(form, saved());
    expect(out.notify_started).toBe('false');
    expect(out.priority_failed).toBe('-1');
    expect(out.cameras).toBe('{"SER-A":"cam a","SER-B":"cam b"}');
    expect(Object.keys(out).filter((k) => k.startsWith('camera'))).toEqual([
      'cameras',
    ]);
  });

  it('writes no cameras as an empty setting', () => {
    expect(toSettings(base(), saved()).cameras).toBe('');
  });

  it('writes the muted serials as one list', () => {
    const form = base();
    form.muted = ['A', 'B'];
    expect(toSettings(form, saved()).muted_printers).toBe('A,B');
  });

  it('round-trips through what the host would send back', () => {
    const form = base();
    form.rules.finished.title = 'Done {file}';
    form.rules.finished.image = 'thumbnail';
    form.muted = ['X'];
    form.cameras = { 'SER-A': 'bench-top' };
    form.connectUrl = 'http://127.0.0.1:8080';

    const back = fromConfig(toSettings(form, saved()));
    expect(back.rules).toEqual(form.rules);
    expect(back.muted).toEqual(form.muted);
    expect(back.cameras).toEqual(form.cameras);
    expect(back.connectUrl).toBe(form.connectUrl);
  });

  it('has no limit on the printers that have a camera', () => {
    const form = base();
    form.cameras = Object.fromEntries(
      Array.from({ length: 40 }, (_, i) => [`S${String(i)}`, `c${String(i)}`]),
    );
    expect(problems(form, saved()).cameras).toBeUndefined();
    expect(
      Object.keys(fromConfig(toSettings(form, saved())).cameras),
    ).toHaveLength(40);
  });

  it('opens on a cameras setting that is not what the page writes', () => {
    for (const text of [
      'SER-A=cam',
      '[1]',
      '{"A":5}',
      '{"":"x"}',
      'null',
      '',
    ]) {
      expect(fromConfig({ cameras: text }).cameras).toEqual({});
    }
  });
});

describe('a template', () => {
  it('accepts the tags of its message type', () => {
    expect(templateProblem('finished', '{file} {energy}')).toBeUndefined();
    expect(templateProblem('attention', '{dialog_text}')).toBeUndefined();
  });

  it('refuses a tag of another type, a stray brace and an empty title', () => {
    expect(templateProblem('started', '{energy}')).toContain('{energy}');
    expect(templateProblem('lost', '{dialog_text}')).toContain('{dialog_text}');
    expect(templateProblem('started', '{file} {')).toBeDefined();
    expect(templateProblem('started', '{File}')).toBeDefined();
  });

  // The default finished message has a line with {energy}. Without Tasmota
  // Plugs the page leaves that line out of a message, so it is no reason to
  // refuse a save (it once was: a fresh install could not be saved).
  it('does not refuse a power tag, whether or not Tasmota Plugs is installed', () => {
    expect(templateProblem('finished', '{energy}')).toBeUndefined();
    expect(templateProblem('finished', 'x {cost}')).toBeUndefined();
    expect(
      templateProblem('finished', DEFAULT_RULES.finished.body, 'body'),
    ).toBeUndefined();
  });

  it('is held to Pushover’s lengths', () => {
    expect(
      templateProblem('started', 'a'.repeat(250), 'title'),
    ).toBeUndefined();
    expect(templateProblem('started', 'a'.repeat(251), 'title')).toBeDefined();
    expect(
      templateProblem('started', 'a'.repeat(1024), 'body'),
    ).toBeUndefined();
    expect(templateProblem('started', 'a'.repeat(1025), 'body')).toBeDefined();
    expect(templateProblem('started', '  ', 'title')).toBeDefined();
  });
});

describe('the preview', () => {
  it('fills the tags with sample values', () => {
    const view = preview('finished', DEFAULT_RULES.finished, true);
    expect(view.title).toBe('Core One: print finished');
    expect(view.body).toBe(
      'benchy.bgcode\nPrinted for 1h 23m\n4.2 m of filament\nUsed 0.42 kWh',
    );
  });

  it('leaves a line out when a tag has no value, as the module does', () => {
    expect(
      fill('{file}\n{nope} x\n\n  {printer}  ', { file: 'a', printer: 'B' }),
    ).toBe('a\nB');
  });

  it('has no power lines while Tasmota Plugs is not installed', () => {
    const view = preview('finished', DEFAULT_RULES.finished, false);
    expect(view.body).not.toContain('Used');
  });
});

describe('what is wrong with the form', () => {
  const ok = (form: Form = base(), s: Saved = saved()) => problems(form, s);

  it('is nothing for the defaults', () => {
    expect(ok()).toEqual({});
  });

  it('wants the token and the user key together', () => {
    const none = saved({ hasToken: false, hasUser: false });
    const onlyToken = { ...base(), token: 'abc' };
    expect(ok(onlyToken, none).pair).toBeDefined();
    expect(ok({ ...onlyToken, user: 'def' }, none).pair).toBeUndefined();
    expect(ok(base(), saved({ hasUser: false })).pair).toBeDefined();
  });

  it('refuses a credential with a space in it', () => {
    expect(ok({ ...base(), token: 'a b' }).token).toBeDefined();
    expect(ok({ ...base(), user: 'a b' }).user).toBeDefined();
  });

  it('wants a picture to have an address and a token for Connect', () => {
    const form = base();
    form.rules.finished.image = 'camera';
    expect(ok(form).connect).toBeDefined();

    form.connectUrl = 'http://127.0.0.1:8080';
    expect(ok(form).connect).toBeDefined();

    expect(ok(form, saved({ hasConnectToken: true })).connect).toBeUndefined();
    expect(ok({ ...form, connectToken: 'tok' }).connect).toBeUndefined();
  });

  it('needs no address for a picture on a message that is switched off', () => {
    const form = base();
    form.rules.cancelled.image = 'camera';
    expect(ok(form).connect).toBeUndefined();
  });

  it('refuses an address that is not one', () => {
    for (const url of [
      'ftp://x',
      'http://',
      'http://u:p@h',
      'http://h/?q=1',
      'h',
    ]) {
      expect(ok({ ...base(), connectUrl: url }).connectUrl, url).toBeDefined();
    }
    expect(
      ok({ ...base(), connectUrl: 'https://connect.lan:8443/' }).connectUrl,
    ).toBeUndefined();
  });

  it('reports each template problem under its message type', () => {
    const form = base();
    form.rules.started.title = '{energy}';
    form.rules.failed.body = '{';
    const found = ok(form);
    expect(found['title.started']).toBeDefined();
    expect(found['body.failed']).toBeDefined();
    expect(found['title.finished']).toBeUndefined();
  });
});

describe('unsaved changes', () => {
  it('are noticed in any part of the form', () => {
    const start = base();
    expect(dirty(start, structuredClone(start))).toBe(false);

    const edits: ((f: Form) => void)[] = [
      (f) => (f.token = 'x'),
      (f) => (f.user = 'x'),
      (f) => (f.connectToken = 'x'),
      (f) => (f.connectUrl = 'http://h'),
      (f) => (f.rules.started.on = false),
      (f) => (f.rules.started.title = 'x'),
      (f) => (f.muted = ['A']),
      (f) => (f.cameras = { A: 'c' }),
    ];
    for (const edit of edits) {
      const changed = structuredClone(start);
      edit(changed);
      expect(dirty(changed, start)).toBe(true);
    }
  });

  it('do not count the order muted printers were ticked in', () => {
    const a = { ...base(), muted: ['A', 'B'] };
    const b = { ...base(), muted: ['B', 'A'] };
    expect(dirty(a, b)).toBe(false);
  });
});

describe('values the host returns typed', () => {
  it('are read as the text they stand for, so a hand-written config is not shown as defaults', () => {
    const form = fromConfig({
      notify_started: false,
      notify_cancelled: true,
      priority_failed: -2,
      priority_started: 1,
      cameras: JSON.stringify({ A: 'bench-top' }),
    });

    expect(form.rules.started.on).toBe(false);
    expect(form.rules.cancelled.on).toBe(true);
    expect(form.rules.failed.priority).toBe(-2);
    expect(form.rules.started.priority).toBe(1);
    expect(form.cameras).toEqual({ A: 'bench-top' });
  });

  it('leave an object or a list at the default', () => {
    expect(fromConfig({ notify_started: [true] }).rules.started.on).toBe(
      DEFAULT_RULES.started.on,
    );
  });
});

describe('the time zone', () => {
  it('is read, written and compared', () => {
    const form = fromConfig({ timezone: 'Asia/Tokyo' });
    expect(form.timezone).toBe('Asia/Tokyo');
    expect(toSettings(form, saved()).timezone).toBe('Asia/Tokyo');
    expect(toSettings(base(), saved()).timezone).toBe('');
    expect(
      toSettings({ ...base(), timezone: 'america/new_york' }, saved()).timezone,
    ).toBe('America/New_York');
    expect(dirty({ ...form, timezone: 'Europe/Berlin' }, form)).toBe(true);
  });

  it('must be a zone the module can load', () => {
    const ok = (timezone: string) =>
      problems({ ...base(), timezone }, saved()).timezone;

    expect(ok('')).toBeUndefined();
    expect(ok('America/New_York')).toBeUndefined();
    expect(ok('UTC')).toBeUndefined();
    expect(ok('Mars/Base')).toBeDefined();
    expect(ok('new york')).toBeDefined();
  });
});

describe('the Prusa Connect address and the ports the manifest allows', () => {
  it('uses the ports module.yaml lists for connect_url', () => {
    const match = /- option: connect_url\n\s+ports: \[([^\]]*)\]/.exec(
      repo('module.yaml'),
    );
    const ports = (match?.[1] ?? '').split(',').map((p) => Number(p.trim()));
    expect([...CONNECT_PORTS]).toEqual(ports);
  });

  it('refuses a port the module may not connect to, and takes the default of the scheme', () => {
    const ok = (connectUrl: string) =>
      problems({ ...base(), connectUrl }, saved()).connectUrl;

    expect(ok('http://127.0.0.1:8080')).toBeUndefined();
    expect(ok('https://connect.lan:8443/')).toBeUndefined();
    expect(ok('http://127.0.0.1')).toBeUndefined();
    expect(ok('https://connect.lan')).toBeUndefined();
    expect(ok('http://127.0.0.1:18080')).toBeDefined();
    expect(ok('https://connect.lan:22')).toBeDefined();
  });
});

describe('the time zone the page starts with', () => {
  it('shows the browser zone but keeps what is saved as the original, so Save turns on', () => {
    const { form, original } = readForm({}, 'Europe/Berlin');
    expect(form.timezone).toBe('Europe/Berlin');
    expect(original.timezone).toBe('');
    expect(dirty(form, original)).toBe(true);
  });

  it('leaves a zone that is saved alone, and is not changed', () => {
    const { form, original } = readForm(
      { timezone: 'Asia/Tokyo' },
      'Europe/Berlin',
    );
    expect(form.timezone).toBe('Asia/Tokyo');
    expect(dirty(form, original)).toBe(false);
  });

  it('makes two forms that do not share anything', () => {
    const { form, original } = readForm({}, 'UTC');
    form.rules.started.on = false;
    expect(original.rules.started.on).toBe(DEFAULT_RULES.started.on);
  });
});

describe('what a message type test tells the person', () => {
  it('names the printer when no picture was asked for', () => {
    expect(
      kindTestOutcome({
        printer: 'MK4S',
        picture: 'none',
        picture_problem: '',
      }),
    ).toEqual({ what: 'plain', printer: 'MK4S' });
  });

  it('names the picture that went with the message', () => {
    expect(
      kindTestOutcome({
        printer: 'MK4S',
        picture: 'camera',
        picture_problem: '',
      }),
    ).toEqual({ what: 'picture', printer: 'MK4S', picture: 'camera' });
  });

  it('gives the reason first when a picture was asked for and not got', () => {
    expect(
      kindTestOutcome({
        printer: 'MK4S',
        picture: 'none',
        picture_problem: 'Prusa Connect answered 401',
      }),
    ).toEqual({ what: 'no_picture', reason: 'Prusa Connect answered 401' });

    // A picture that went while another camera failed says both.
    expect(
      kindTestOutcome({
        printer: 'Core One',
        picture: 'camera',
        picture_problem: 'the camera x of MK4S: down',
      }),
    ).toEqual({
      what: 'picture_but',
      printer: 'Core One',
      picture: 'camera',
      reason: 'the camera x of MK4S: down',
    });
  });
});
