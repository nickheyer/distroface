<script lang="ts">
  let { uploadId, progress } = $props<{
      uploadId: string;
      progress: number;
  }>();
</script>

<div class="space-y-2">
  <div class="flex justify-between text-sm text-gray-600">
      <span>Uploading...</span>
      <span>{Math.round(progress)}%</span>
  </div>
  <div class="h-2 w-full bg-gray-200 rounded-full overflow-hidden">
      <div 
          class="h-full bg-blue-600 transition-all duration-300 ease-in-out"
          style:width="{progress}%"
      ></div>
  </div>
</div>
