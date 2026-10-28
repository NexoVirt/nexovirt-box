<script>
	import { Progress as ProgressPrimitive } from "bits-ui";
	import { cn } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		max = 100,
		value,
		indicatorClass,
		...restProps
	} = $props();
</script>

<ProgressPrimitive.Root
	bind:ref
	data-slot="progress"
	class={cn("bg-muted h-1 rounded-full relative flex w-full items-center overflow-x-hidden", className)}
	{value}
	{max}
	{...restProps}
>
	<div
		data-slot="progress-indicator"
		class={cn("bg-primary size-full flex-1 transition-all duration-500", indicatorClass)}
		style="transform: translateX(-{100 - (100 * (value ?? 0)) / (max ?? 1)}%)"
	></div>
</ProgressPrimitive.Root>