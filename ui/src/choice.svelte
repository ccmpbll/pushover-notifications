<script lang="ts">
  import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
  } from '@prusa/connect-ui/primitives';

  import { fromOption, toOption } from './choice';

  interface Option {
    readonly value: string;
    readonly label: string;
  }

  interface Props {
    value: string;
    options: readonly Option[];
    label: string;
    onchange: (value: string) => void;
    disabled?: boolean;
  }

  let { value, options, label, onchange, disabled = false }: Props = $props();

  // An empty value cannot be an option of the select (see choice.ts).
  const held = $derived(
    options.map((o) => ({ ...o, value: toOption(o.value) })),
  );
</script>

<Select
  value={toOption(value)}
  items={held}
  onValueChange={(chosen: string) => onchange(fromOption(chosen))}
  {disabled}
>
  <SelectTrigger aria-label={label} class="w-full">
    <SelectValue />
  </SelectTrigger>
  <SelectContent>
    {#each held as option (option.value)}
      <SelectItem value={option.value} label={option.label}>
        {option.label}
      </SelectItem>
    {/each}
  </SelectContent>
</Select>
