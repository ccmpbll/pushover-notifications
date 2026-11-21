<script lang="ts">
  import { tick } from 'svelte';

  import {
    Button,
    Chip,
    Input,
    Label,
    Switch,
    Textarea,
  } from '@prusa/connect-ui/primitives';

  import Choice from './choice.svelte';
  import { heldHost } from './host';
  import {
    DEFAULT_RULES,
    MAX_PRIORITY,
    MIN_PRIORITY,
    NEEDS_TASMOTA,
    TAGS_FOR,
    preview,
    type Image,
    type Kind,
    type Rule,
  } from './form';
  import { SOUNDS } from './sounds';

  interface Props {
    kind: Kind;
    rule: Rule;
    tasmota: boolean;
    // Why the test cannot be sent now, or undefined when it can.
    testBlocked?: string | undefined;
    // The page has changes that are not saved.
    unsaved?: boolean;
    ontest: (kind: Kind) => Promise<{ ok: boolean; text: string }>;
    titleProblem?: string | undefined;
    bodyProblem?: string | undefined;
    onchange: () => void;
  }

  let {
    kind,
    rule = $bindable(),
    tasmota,
    testBlocked,
    unsaved = false,
    ontest,
    titleProblem,
    bodyProblem,
    onchange,
  }: Props = $props();

  const t = heldHost().t;

  let open = $state(false);
  let testing = $state(false);
  let outcome = $state<{ ok: boolean; text: string } | undefined>();

  // The result describes the rule as it was saved. Once the page has changes
  // the line no longer does, so it goes.
  $effect(() => {
    if (unsaved) outcome = undefined;
  });

  const runTest = async (): Promise<void> => {
    testing = true;
    outcome = undefined;
    try {
      outcome = await ontest(kind);
    } finally {
      testing = false;
    }
  };
  // Which of the two boxes a tag button writes into: the one used last.
  let lastBox = $state<'title' | 'body'>('body');

  const id = (part: string): string => `pushover-${kind}-${part}`;

  const priorities = Array.from(
    { length: MAX_PRIORITY - MIN_PRIORITY + 1 },
    (_, i) => {
      const level = MIN_PRIORITY + i;

      return {
        value: String(level),
        label: t(`pushover.priority.${String(level)}`),
      };
    },
  );
  const sounds = SOUNDS.map(([value, label]) => ({ value, label }));
  const images = $derived(
    (['none', 'thumbnail', 'camera'] as const).map((value) => ({
      value,
      label: t(`pushover.image.${value}`),
    })),
  );

  const view = $derived(preview(kind, rule, tasmota));
  const changed = $derived(
    rule.title !== DEFAULT_RULES[kind].title ||
      rule.body !== DEFAULT_RULES[kind].body,
  );

  const typed = (event: Event): string =>
    event.target instanceof HTMLInputElement ||
    event.target instanceof HTMLTextAreaElement
      ? event.target.value
      : '';

  /** Puts a tag in the box last used, where the cursor is. */
  const insert = async (tag: string): Promise<void> => {
    const box = document.getElementById(id(lastBox));
    const text = `{${tag}}`;

    if (box instanceof HTMLInputElement || box instanceof HTMLTextAreaElement) {
      const from = box.selectionStart ?? box.value.length;
      const to = box.selectionEnd ?? from;
      const next = box.value.slice(0, from) + text + box.value.slice(to);
      if (lastBox === 'title') rule.title = next;
      else rule.body = next;
      onchange();
      // Svelte writes the new value into the box on its next update. The caret
      // is set after that, or the write would move it to the end.
      await tick();
      box.focus();
      box.setSelectionRange(from + text.length, from + text.length);

      return;
    }

    rule.body += text;
    onchange();
  };

  const restore = (): void => {
    rule.title = DEFAULT_RULES[kind].title;
    rule.body = DEFAULT_RULES[kind].body;
    onchange();
  };

  const tagDisabled = (tag: string): boolean =>
    NEEDS_TASMOTA.includes(tag) && !tasmota;
</script>

<div
  class="grid gap-3 border-t py-3 first:border-t-0 md:grid-cols-12 md:items-start"
  data-off={!rule.on}
>
  <div class="flex items-start gap-3 md:col-span-6 md:min-h-8 md:items-center">
    <Switch
      checked={rule.on}
      aria-label={t(`pushover.kind.${kind}.switch`)}
      onCheckedChange={(on: boolean) => {
        rule.on = on;
        onchange();
      }}
    />
    <div class="flex min-w-0 flex-col">
      <span class="text-sm font-medium">{t(`pushover.kind.${kind}`)}</span>
      <div class="flex flex-wrap items-center gap-x-3">
        <Button
          variant="link"
          size="sm"
          class="h-auto w-fit p-0 text-xs"
          onclick={() => (open = !open)}
        >
          {open ? t('pushover.text.hide') : t('pushover.text.customize')}
          {#if titleProblem !== undefined || bodyProblem !== undefined}
            · {t('pushover.text.needs_fix')}
          {:else if changed && !open}
            · {t('pushover.text.changed')}
          {/if}
        </Button>
        <Button
          variant="link"
          size="sm"
          class="h-auto w-fit p-0 text-xs"
          title={testBlocked}
          disabled={testBlocked !== undefined || testing}
          onclick={() => void runTest()}
        >
          {testing
            ? t('pushover.kind_test.sending')
            : t('pushover.kind_test.send')}
        </Button>
      </div>
      {#if outcome}
        <p
          class={[
            'text-xs',
            outcome.ok ? 'text-muted-foreground' : 'text-destructive-text',
          ]}
        >
          {outcome.text}
        </p>
      {/if}
    </div>
  </div>

  <div class="flex flex-col gap-1 md:col-span-2">
    <span class="text-xs text-muted-foreground md:hidden">
      {t('pushover.priority')}
    </span>
    <Choice
      value={String(rule.priority)}
      options={priorities}
      label={t('pushover.priority.for', { kind: t(`pushover.kind.${kind}`) })}
      disabled={!rule.on}
      onchange={(value: string) => {
        rule.priority = Number(value);
        onchange();
      }}
    />
  </div>

  <div class="flex flex-col gap-1 md:col-span-4">
    <span class="text-xs text-muted-foreground md:hidden">
      {t('pushover.sound')}
    </span>
    <Choice
      value={rule.sound}
      options={sounds}
      label={t('pushover.sound.for', { kind: t(`pushover.kind.${kind}`) })}
      disabled={!rule.on}
      onchange={(value: string) => {
        rule.sound = value;
        onchange();
      }}
    />
  </div>

  {#if open}
    <div
      class="flex flex-col gap-3 rounded-md border bg-muted p-3 md:col-span-12"
    >
      <div class="flex flex-col gap-1">
        <Label for={id('title')}>{t('pushover.text.title')}</Label>
        <Input
          id={id('title')}
          value={rule.title}
          onfocus={() => (lastBox = 'title')}
          oninput={(event: Event) => {
            rule.title = typed(event);
            onchange();
          }}
          aria-invalid={titleProblem !== undefined}
        />
        {#if titleProblem}
          <p class="text-xs text-destructive-text">{titleProblem}</p>
        {/if}
      </div>

      <div class="flex flex-col gap-1">
        <Label for={id('body')}>{t('pushover.text.body')}</Label>
        <Textarea
          id={id('body')}
          value={rule.body}
          rows={4}
          onfocus={() => (lastBox = 'body')}
          oninput={(event: Event) => {
            rule.body = typed(event);
            onchange();
          }}
          aria-invalid={bodyProblem !== undefined}
        />
        {#if bodyProblem}
          <p class="text-xs text-destructive-text">{bodyProblem}</p>
        {/if}
      </div>

      <div class="flex flex-col gap-1">
        <span class="text-sm font-medium">{t('pushover.text.tags')}</span>
        <div class="flex flex-wrap gap-1">
          {#each TAGS_FOR[kind] as tag (tag)}
            {#if tagDisabled(tag)}
              <span
                class="rounded-full border px-2 font-mono text-xs text-muted-foreground opacity-50"
                title={t('pushover.tag.needs_tasmota')}>{`{${tag}}`}</span
              >
            {:else}
              <Chip
                class="font-mono"
                label={t(`pushover.tag.${tag}`)}
                onclick={() => void insert(tag)}>{`{${tag}}`}</Chip
              >
            {/if}
          {/each}
        </div>
        <p class="text-xs text-muted-foreground">
          {t('pushover.text.line_rule')}
        </p>
        {#if !tasmota && TAGS_FOR[kind].some( (tag) => NEEDS_TASMOTA.includes(tag) )}
          <p class="text-xs text-muted-foreground">
            {t('pushover.tag.needs_tasmota.note')}
          </p>
        {/if}
      </div>

      {#if kind !== 'lost'}
        <div class="flex max-w-xs flex-col gap-1">
          <Label>{t('pushover.image')}</Label>
          <Choice
            value={rule.image}
            options={images}
            label={t('pushover.image')}
            onchange={(value: string) => {
              rule.image = value as Image;
              onchange();
            }}
          />
          <p class="text-xs text-muted-foreground">
            {t('pushover.image.hint')}
          </p>
        </div>
      {/if}

      <div class="flex flex-col gap-1">
        <span class="text-sm font-medium">{t('pushover.text.preview')}</span>
        <div
          class="rounded-xl flex max-w-sm flex-col gap-0.5 border bg-background px-3 py-2"
        >
          <span class="text-xs tracking-wide text-muted-foreground uppercase">
            {t('pushover.text.preview.app')}
          </span>
          <span class="text-sm font-semibold">{view.title}</span>
          <span class="text-sm whitespace-pre-line">{view.body}</span>
          {#if rule.image !== 'none'}
            <div
              class="rounded-lg mt-1 flex h-20 items-center justify-center bg-muted text-xs text-muted-foreground"
            >
              {t(`pushover.image.sample.${rule.image}`)}
            </div>
          {/if}
        </div>
      </div>

      <div>
        <Button variant="ghost" size="sm" disabled={!changed} onclick={restore}>
          {t('pushover.text.restore')}
        </Button>
      </div>
    </div>
  {/if}
</div>
