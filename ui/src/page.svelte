<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Button,
    Card,
    CardContent,
    Input,
    Label,
    PageHeader,
    Switch,
  } from '@prusa/connect-ui/primitives';
  import { bind } from 'pushover-notifications-client';

  import { hideApplying, showApplying } from './applying';
  import { isRefused } from './refused';
  import Choice from './choice.svelte';
  import {
    KINDS,
    browserZone,
    dirty,
    kindTestOutcome,
    readForm,
    problems,
    toSettings,
    type Form,
    type Kind,
  } from './form';
  import { heldHost } from './host';
  import MessageRow from './message-row.svelte';

  const host = heldHost();
  const t = host.t;

  // The namespace Connect derives for this module, which is how its instance is
  // found among the server's resources whatever the operator named it, and the
  // one the Tasmota Plugs extension has: with it, the power tags have values.
  const NAMESPACE = 'com.github.aux.ccmpbll.pushover-notifications';
  const TASMOTA = 'com.github.aux.ccmpbll.tasmota-plugs';
  const KIND = 'sink';
  // How long the extension needs to restart and take new settings.
  const RESTART_MS = 4000;

  type Resource = Awaited<ReturnType<typeof host.client.getResource>>;
  interface Camera {
    readonly name: string;
    readonly printer: string;
  }

  let loading = $state(true);
  let saving = $state(false);
  let failure = $state<string | undefined>();
  let resource = $state<Resource | undefined>();
  let form = $state<Form | undefined>();
  let original = $state<Form | undefined>();
  let tasmota = $state(false);
  let cameras = $state<Camera[]>([]);
  let test = $state<'idle' | 'busy' | 'ok' | 'bad'>('idle');
  let testReason = $state('');
  // Connect refused a save because of the signed-in person's role.
  let readOnly = $state(false);
  // Connect refused a test message because of the signed-in person's role.
  let testRefused = $state(false);

  const messageOf = (error: unknown): string =>
    error instanceof Error ? error.message : String(error);

  const typed = (event: Event): string =>
    event.target instanceof HTMLInputElement ? event.target.value : '';

  const load = async (): Promise<void> => {
    try {
      const listed = await host.client.listResources({}, host.signal);
      const items = listed.items ?? [];
      const found = items.find(
        (r) => r.namespace === NAMESPACE && r.kind === KIND,
      );
      if (!found) throw new Error(t('pushover.error.not_found'));

      tasmota = items.some((r) => r.namespace === TASMOTA);

      const spec = await host.client.getResource(
        { path: { kind: KIND, name: found.name } },
        host.signal,
      );
      const { form: read, original: stored } = readForm(
        spec.config,
        browserZone(),
      );

      try {
        const settings = await host.client.listCameraSettings({}, host.signal);
        cameras = (settings.items ?? []).map((c) => ({
          name: c.name,
          printer: typeof c.config.printer === 'string' ? c.config.printer : '',
        }));
      } catch {
        // The cameras are a convenience: without them every printer has none.
        cameras = [];
      }

      resource = spec;
      form = read;
      original = stored;
      failure = undefined;
      // The page was read, so the extension is up. After a save Connect drops
      // this page and puts a new one up when the extension is back, and the
      // old page's timer died with it, so the notice is taken away here and
      // not left to its 30 second limit.
      hideApplying();
    } catch (error) {
      if (!host.signal.aborted)
        failure = t('pushover.error.load', { reason: messageOf(error) });
    } finally {
      loading = false;
    }
  };

  void load();

  const found = $derived(form ? problems(form, form.saved) : {});
  const valid = $derived(Object.keys(found).length === 0);
  const changed = $derived(
    form !== undefined && original !== undefined && dirty(form, original),
  );
  // The test sends with what is saved, so it waits for the changes to be saved.
  const unsavedCredentials = $derived(
    form !== undefined && (form.token !== '' || form.user !== ''),
  );
  const configured = $derived(
    form !== undefined && form.saved.hasToken && form.saved.hasUser,
  );

  const printers = $derived(
    host.fleet.devices
      .filter((d) => d.serial !== '')
      .map((d) => ({ id: d.id, serial: d.serial, name: d.name })),
  );
  // Printers that are muted or have a camera but that Connect no longer lists.
  const gone = $derived(
    form ? form.muted.filter((s) => !printers.some((p) => p.serial === s)) : [],
  );

  const cameraOptions = (printerId: string) => [
    { value: '', label: t('pushover.printers.no_camera') },
    ...[...cameras]
      .sort(
        (a, b) =>
          Number(b.printer === printerId) - Number(a.printer === printerId) ||
          a.name.localeCompare(b.name),
      )
      .map((c) => ({
        value: c.name,
        label:
          c.printer === printerId
            ? t('pushover.printers.camera_here', { name: c.name })
            : c.name,
      })),
  ];

  /** Sets one of the text fields of the form. */
  const edit = (
    field: 'token' | 'user' | 'connectUrl' | 'connectToken' | 'timezone',
    value: string,
  ): void => {
    if (form) form[field] = value;
  };

  const setMuted = (serial: string, notified: boolean): void => {
    if (!form) return;
    form.muted = notified
      ? form.muted.filter((s) => s !== serial)
      : [...form.muted, serial];
  };

  const setCamera = (serial: string, name: string): void => {
    if (!form) return;
    form.cameras =
      name === ''
        ? Object.fromEntries(
            Object.entries(form.cameras).filter(([key]) => key !== serial),
          )
        : { ...form.cameras, [serial]: name };
  };

  const discard = (): void => {
    if (!original) return;
    form = structuredClone($state.snapshot(original));
    test = 'idle';
  };

  // One message type's test, written from its saved rule. The text says what
  // went out, and why a picture the rule asks for did not.
  const kindTestBlocked = $derived.by(() => {
    if (testRefused) return t('pushover.error.test_refused');
    if (!configured) return t('pushover.kind_test.save_first');
    if (changed) return t('pushover.kind_test.unsaved');

    return undefined;
  });

  const sendKindTest = async (
    kind: Kind,
  ): Promise<{ ok: boolean; text: string }> => {
    try {
      const result = await bind(host.client).sendTest(
        { body: { kind } },
        host.signal,
      );
      const outcome = kindTestOutcome(result);
      switch (outcome.what) {
        case 'no_picture':
          return {
            ok: true,
            text: t('pushover.kind_test.no_picture', {
              reason: outcome.reason,
            }),
          };
        case 'picture_but':
          return {
            ok: true,
            text: t('pushover.kind_test.ok_picture_but', {
              printer: outcome.printer,
              picture: t(`pushover.image.sample.${outcome.picture}`),
              reason: outcome.reason,
            }),
          };
        case 'picture':
          return {
            ok: true,
            text: t('pushover.kind_test.ok_picture', {
              printer: outcome.printer,
              picture: t(`pushover.image.sample.${outcome.picture}`),
            }),
          };
        default:
          return {
            ok: true,
            text: t('pushover.kind_test.ok', { printer: outcome.printer }),
          };
      }
    } catch (error) {
      if (isRefused(error)) testRefused = true;

      return {
        ok: false,
        text: isRefused(error)
          ? t('pushover.error.test_refused')
          : messageOf(error),
      };
    }
  };

  const sendTest = async (): Promise<void> => {
    test = 'busy';
    try {
      await bind(host.client).sendTest({}, host.signal);
      test = 'ok';
    } catch (error) {
      if (host.signal.aborted) return;
      test = 'bad';
      if (isRefused(error)) testRefused = true;
      testReason = isRefused(error)
        ? t('pushover.error.test_refused')
        : messageOf(error);
    }
  };

  const save = async (): Promise<void> => {
    if (!resource || !form || !valid || saving) return;
    saving = true;
    failure = undefined;

    try {
      await host.client.putResource(
        {
          path: { kind: KIND, name: resource.name },
          // The write is refused without the version it was read at, so two
          // people editing the same instance cannot overwrite each other.
          header: { 'If-Match': `"${resource.version}"` },
          body: {
            type: resource.type,
            enabled: resource.enabled,
            required: resource.required,
            config: { ...resource.config, ...toSettings(form, form.saved) },
          },
        },
        host.signal,
      );
      // The extension restarts to take the new settings, and Connect takes its
      // page down meanwhile. Cover the app with a notice, then read it again.
      showApplying(t('pushover.applying.title'), t('pushover.applying.detail'));
      const timer = setTimeout(() => {
        hideApplying();
        test = 'idle';
        void load();
      }, RESTART_MS);
      host.signal.addEventListener('abort', () => clearTimeout(timer), {
        once: true,
      });
    } catch (error) {
      if (isRefused(error)) {
        readOnly = true;
        failure = t('pushover.error.save_refused');
      } else if (!host.signal.aborted)
        failure = t('pushover.error.save', { reason: messageOf(error) });
    } finally {
      saving = false;
    }
  };

  const noop = (): void => undefined;
</script>

<div class="flex flex-col gap-4">
  <PageHeader title={t('pushover.page.title')} />

  {#if loading}
    <p class="text-sm text-muted-foreground">{t('pushover.loading')}</p>
  {/if}

  {#if failure}
    <Alert variant="destructive">
      <AlertDescription>{failure}</AlertDescription>
    </Alert>
  {/if}

  {#if form && resource}
    {#if !configured && !unsavedCredentials}
      <p class="text-sm text-muted-foreground">
        {t('pushover.status.not_set_up')}
      </p>
    {/if}

    <Card>
      <CardContent class="flex flex-col gap-4">
        <div class="flex flex-col gap-1">
          <h2 class="text-base font-medium">{t('pushover.account.title')}</h2>
          <p class="text-sm text-muted-foreground">
            {t('pushover.account.description')}
          </p>
        </div>

        <div class="grid gap-3 md:grid-cols-2">
          <div class="flex flex-col gap-1">
            <Label for="pushover-token">{t('pushover.account.token')}</Label>
            <Input
              id="pushover-token"
              type="password"
              autocomplete="off"
              spellcheck={false}
              value={form.token}
              placeholder={form.saved.hasToken ? '••••••••••••' : ''}
              oninput={(event: Event) => edit('token', typed(event))}
              aria-invalid={found.token !== undefined}
            />
            {#if form.saved.hasToken && form.token === ''}
              <p class="text-xs text-muted-foreground">
                {t('pushover.account.saved')}
              </p>
            {/if}
            {#if found.token}
              <p class="text-xs text-destructive-text">{found.token}</p>
            {/if}
          </div>
          <div class="flex flex-col gap-1">
            <Label for="pushover-user">{t('pushover.account.user')}</Label>
            <Input
              id="pushover-user"
              type="password"
              autocomplete="off"
              spellcheck={false}
              value={form.user}
              placeholder={form.saved.hasUser ? '••••••••••••' : ''}
              oninput={(event: Event) => edit('user', typed(event))}
              aria-invalid={found.user !== undefined}
            />
            {#if form.saved.hasUser && form.user === ''}
              <p class="text-xs text-muted-foreground">
                {t('pushover.account.saved')}
              </p>
            {/if}
            {#if found.user}
              <p class="text-xs text-destructive-text">{found.user}</p>
            {/if}
          </div>
        </div>
        {#if found.pair}
          <p class="text-xs text-destructive-text">{found.pair}</p>
        {/if}

        <div class="flex flex-wrap items-center gap-3">
          <Button
            variant="secondary"
            size="sm"
            pending={test === 'busy'}
            disabled={!configured ||
              unsavedCredentials ||
              testRefused ||
              test === 'busy'}
            onclick={() => void sendTest()}
          >
            {t('pushover.test.send')}
          </Button>
          <span
            class={[
              'text-xs',
              test === 'bad'
                ? 'text-destructive-text'
                : 'text-muted-foreground',
            ]}
          >
            {#if !configured}
              {t('pushover.test.save_first')}
            {:else if unsavedCredentials}
              {t('pushover.test.unsaved')}
            {:else if test === 'ok'}
              {t('pushover.test.ok')}
            {:else if test === 'bad'}
              {testReason}
            {:else}
              {t('pushover.test.hint')}
            {/if}
          </span>
        </div>
      </CardContent>
    </Card>

    <Card>
      <CardContent class="flex flex-col gap-2">
        <div class="flex flex-col gap-1">
          <h2 class="text-base font-medium">{t('pushover.messages.title')}</h2>
          <p class="text-sm text-muted-foreground">
            {t('pushover.messages.description')}
          </p>
        </div>

        <div class="flex max-w-xs flex-col gap-1 pt-1">
          <Label for="pushover-timezone"
            >{t('pushover.messages.timezone')}</Label
          >
          <Input
            id="pushover-timezone"
            value={form.timezone}
            placeholder={t('pushover.messages.timezone.placeholder')}
            oninput={(event: Event) => edit('timezone', typed(event))}
            aria-invalid={found.timezone !== undefined}
          />
          <p class="text-xs text-muted-foreground">
            {t('pushover.messages.timezone.hint')}
          </p>
          {#if found.timezone}
            <p class="text-xs text-destructive-text">{found.timezone}</p>
          {/if}
        </div>

        <div
          class="hidden gap-3 text-xs text-muted-foreground md:grid md:grid-cols-12"
        >
          <span class="md:col-span-6">{t('pushover.messages.tell_me')}</span>
          <span class="md:col-span-2">{t('pushover.priority')}</span>
          <span class="md:col-span-4">{t('pushover.sound')}</span>
        </div>

        {#each ['prints', 'problems'] as group (group)}
          <h3
            class="pt-3 text-xs font-semibold tracking-wide text-muted-foreground uppercase"
          >
            {t(`pushover.group.${group}`)}
          </h3>
          {#each KINDS.filter((k) => (group === 'prints') === ['started', 'finished', 'failed', 'cancelled'].includes(k)) as kind (kind)}
            <MessageRow
              {kind}
              bind:rule={form.rules[kind]}
              {tasmota}
              testBlocked={kindTestBlocked}
              unsaved={changed}
              ontest={sendKindTest}
              titleProblem={found[`title.${kind}`]}
              bodyProblem={found[`body.${kind}`]}
              onchange={noop}
            />
          {/each}
        {/each}
      </CardContent>
    </Card>

    <Card>
      <CardContent class="flex flex-col gap-3">
        <div class="flex flex-col gap-1">
          <h2 class="text-base font-medium">{t('pushover.printers.title')}</h2>
          <p class="text-sm text-muted-foreground">
            {t('pushover.printers.description')}
          </p>
        </div>

        {#if printers.length === 0 && gone.length === 0}
          <p class="text-sm text-muted-foreground">
            {t('pushover.printers.none')}
          </p>
        {/if}

        {#each printers as printer (printer.serial)}
          <div
            class="grid gap-2 border-t py-2 first:border-t-0 md:grid-cols-12 md:items-center"
          >
            <div class="flex items-center gap-3 md:col-span-7">
              <Switch
                checked={!form.muted.includes(printer.serial)}
                aria-label={t('pushover.printers.messages_about', {
                  name: printer.name,
                })}
                onCheckedChange={(on: boolean) => setMuted(printer.serial, on)}
              />
              <div class="flex min-w-0 flex-col">
                <span class="text-sm font-medium">{printer.name}</span>
                <span class="text-xs text-muted-foreground"
                  >{printer.serial}</span
                >
              </div>
            </div>
            <div class="md:col-span-5">
              <Choice
                value={form.cameras[printer.serial] ?? ''}
                options={cameraOptions(printer.id)}
                label={t('pushover.printers.camera_for', {
                  name: printer.name,
                })}
                onchange={(name: string) => setCamera(printer.serial, name)}
              />
            </div>
          </div>
        {/each}

        {#each gone as serial (serial)}
          <div class="flex items-center gap-3 border-t py-2">
            <Switch
              checked={false}
              aria-label={t('pushover.printers.messages_about', {
                name: serial,
              })}
              onCheckedChange={(on: boolean) => setMuted(serial, on)}
            />
            <div class="flex flex-col">
              <span class="text-sm font-medium">
                {serial}
                <span class="text-xs text-muted-foreground"
                  >· {t('pushover.printers.gone')}</span
                >
              </span>
              <span class="text-xs text-muted-foreground"
                >{t('pushover.printers.gone_note')}</span
              >
            </div>
          </div>
        {/each}
      </CardContent>
    </Card>

    <Card>
      <CardContent class="flex flex-col gap-3">
        <div class="flex flex-col gap-1">
          <h2 class="text-base font-medium">{t('pushover.connect.title')}</h2>
          <p class="text-sm text-muted-foreground">
            {t('pushover.connect.description')}
          </p>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <div class="flex flex-col gap-1">
            <Label for="pushover-connect-url">{t('pushover.connect.url')}</Label
            >
            <Input
              id="pushover-connect-url"
              value={form.connectUrl}
              placeholder={t('pushover.connect.url.placeholder')}
              oninput={(event: Event) => edit('connectUrl', typed(event))}
              aria-invalid={found.connectUrl !== undefined}
            />
            {#if found.connectUrl}
              <p class="text-xs text-destructive-text">{found.connectUrl}</p>
            {/if}
          </div>
          <div class="flex flex-col gap-1">
            <Label for="pushover-connect-token"
              >{t('pushover.connect.token')}</Label
            >
            <Input
              id="pushover-connect-token"
              type="password"
              autocomplete="off"
              spellcheck={false}
              value={form.connectToken}
              placeholder={form.saved.hasConnectToken ? '••••••••••••' : ''}
              oninput={(event: Event) => edit('connectToken', typed(event))}
              aria-invalid={found.connectToken !== undefined}
            />
            {#if form.saved.hasConnectToken && form.connectToken === ''}
              <p class="text-xs text-muted-foreground">
                {t('pushover.connect.saved')}
              </p>
            {/if}
            {#if found.connectToken}
              <p class="text-xs text-destructive-text">
                {found.connectToken}
              </p>
            {/if}
          </div>
        </div>
        {#if found.connect}
          <p class="text-xs text-destructive-text">{found.connect}</p>
        {/if}
        <p class="text-xs text-muted-foreground">{t('pushover.connect.how')}</p>
      </CardContent>
    </Card>

    <div
      class="sticky bottom-0 flex flex-wrap items-center justify-between gap-3 border-t bg-background py-3"
    >
      <span class="text-xs text-muted-foreground">
        {changed ? t('pushover.save.unsaved') : t('pushover.save.restart')}
      </span>
      <div class="flex items-center gap-3">
        <Button variant="ghost" size="sm" disabled={!changed} onclick={discard}>
          {t('pushover.save.discard')}
        </Button>
        <Button
          size="sm"
          pending={saving}
          disabled={saving || !changed || !valid || readOnly}
          onclick={() => void save()}
        >
          {t('pushover.save.save')}
        </Button>
      </div>
    </div>
  {/if}
</div>
