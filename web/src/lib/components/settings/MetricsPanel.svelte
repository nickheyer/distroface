<script lang="ts">
  import { auth, api } from "$lib/stores/auth.svelte";
  import "@carbon/charts-svelte/styles.css";
  import {
    LineChart,
    BarChartStacked,
    DonutChart,
  } from "@carbon/charts-svelte";
  import type {
    ChartTabularData,
    LineChartOptions,
    BarChartOptions,
    DonutChartOptions,
  } from "@carbon/charts/interfaces";
  import { ScaleTypes } from "@carbon/charts/interfaces";
  import {
    Activity,
    HardDrive,
    ArrowUp,
    ArrowDown,
    AlertCircle,
    Loader2,
  } from "lucide-svelte";

  interface BlobMetrics {
    total: number;
    failed: number;
    inProgress: number;
    bytesProcessed: number;
    avgDuration: number;
  }

  interface PerformanceMetrics {
    avgUploadSpeed: number;
    avgDownloadSpeed: number;
    diskUsage: number;
    diskTotal: number;
    cpuUsage: number;
    memoryUsage: number;
    memoryTotal: number;
  }

  interface TimeSeriesPoint {
    timestamp: string;
    uploadSpeed: number;
    downloadSpeed: number;
    activeUploads: number;
  }

  interface MetricsData {
    blobUploads: BlobMetrics;
    blobDownloads: BlobMetrics;
    performance: PerformanceMetrics;
    timeseriesData: TimeSeriesPoint[];
  }

  // STATES
  let metrics = $state<MetricsData | null>(null);
  let error = $state<string | null>(null);
  let loading = $state(true);
  let pollInterval = $state<ReturnType<typeof setInterval> | null>(null);

  // CHART OPTIONS
  const lineChartOptions: LineChartOptions = {
    title: "Transfer Speeds Over Time",
    axes: {
      left: {
        title: "Speed (MB/s)",
        mapsTo: "value",
        includeZero: true,
      },
      bottom: {
        title: "Time",
        mapsTo: "date",
        scaleType: ScaleTypes.TIME,
      },
    },
    curve: "curveMonotoneX",
    height: "400px",
    theme: "white",
    legend: {
      alignment: "center",
    },
    grid: {
      x: {
        enabled: false,
      },
    },
    tooltip: {
      showTotal: false,
    },
    timeScale: {
      addSpaceOnEdges: 0,
    },
  };

  const transferStatsOptions: BarChartOptions = {
    title: "Transfer Statistics",
    axes: {
      left: {
        title: "Count",
        mapsTo: "value",
        includeZero: true,
      },
      bottom: {
        title: "Type",
        mapsTo: "group",
        scaleType: ScaleTypes.LABELS,
      },
    },
    height: "400px",
    theme: "white",
  };

  const storageDonutOptions: DonutChartOptions = {
    title: "Storage Usage",
    resizable: true,
    height: "400px",
    theme: "white",
    legend: {
      alignment: "center",
    },
    donut: {
      center: {
        label: "Total",
      },
      alignment: "center",
    },
  };

  async function fetchMetrics() {
    try {
      const response = await api.get("/api/v1/settings/metrics");
      metrics = await response.json();
      error = null;
    } catch (err) {
      error = err instanceof Error ? err.message : "Failed to fetch metrics";
    } finally {
      loading = false;
    }
  }

  function formatBytes(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${(bytes / Math.pow(1024, i)).toFixed(2)} ${units[i]}`;
  }

  function formatSpeed(bytesPerSec: number): string {
    const mbPerSec = bytesPerSec / (1024 * 1024);
    return `${mbPerSec.toFixed(2)} MB/s`;
  }

  // TIMESERIES DATA (LINE CHART)
  let lineChartData: ChartTabularData = $derived(
    (() => {
      if (!metrics?.timeseriesData) return [];
      return metrics.timeseriesData.flatMap((point) => {
        return [
          {
            group: "Upload Speed",
            date: new Date(point.timestamp),
            value: Math.max(0, point.uploadSpeed),
          },
          {
            group: "Download Speed",
            date: new Date(point.timestamp),
            value: Math.max(0, point.downloadSpeed),
          },
        ];
      });
    })()
  );

  // TRANSFER STATS DATA (STACKED BAR)
  let transferStatsData: ChartTabularData = $derived(
    (() => {
      if (!metrics) return [];
      return [
        {
          group: "Uploads",
          key: "Total",
          value: metrics.blobUploads.total,
        },
        {
          group: "Uploads",
          key: "Successful",
          value: metrics.blobUploads.total - metrics.blobUploads.failed,
        },
        {
          group: "Uploads",
          key: "Failed",
          value: metrics.blobUploads.failed,
        },
        {
          group: "Downloads",
          key: "Total",
          value: metrics.blobDownloads.total,
        },
        {
          group: "Downloads",
          key: "Successful",
          value: metrics.blobDownloads.total - metrics.blobDownloads.failed,
        },
        {
          group: "Downloads",
          key: "Failed",
          value: metrics.blobDownloads.failed,
        },
      ];
    })()
  );

  // STORAGE DATA (DONUT CHART)
  let storageData: ChartTabularData = $derived(
  (() => {
    if (!metrics) return [];
    // CONVERT TO GB
    const usedGB = metrics.performance.diskUsage / (1024 ** 3);
    const totalGB = metrics.performance.diskTotal / (1024 ** 3);
    const freeGB = totalGB - usedGB;
    return [
      {
        group: "Used",
        value: usedGB,
        label: `${usedGB.toFixed(1)} GB`,
      },
      {
        group: "Free",
        value: freeGB,
        label: `${freeGB.toFixed(1)} GB`,
      },
    ];
  })()
);

  $effect(() => {
    fetchMetrics(); // POLLING EVERY 10 SECONDS
    pollInterval = setInterval(fetchMetrics, 10 * 1000);

    return () => {
      if (pollInterval) clearInterval(pollInterval);
    };
  });
</script>

<div class="space-y-6">
  {#if error}
    <div class="rounded-md bg-red-50 p-4">
      <div class="flex">
        <AlertCircle class="h-5 w-5 text-red-400" />
        <div class="ml-3">
          <p class="text-sm font-medium text-red-800">{error}</p>
        </div>
      </div>
    </div>
  {:else if loading && !metrics}
    <div class="flex justify-center py-12">
      <Loader2 class="h-8 w-8 animate-spin text-blue-500" />
    </div>
  {:else if metrics}
    <!-- SUMMARY CARDS -->
    <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-4">
      <!-- STORAGE -->
      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <HardDrive class="h-6 w-6 text-gray-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">
                  Storage Used
                </dt>
                <dd class="flex items-baseline">
                  <div class="text-2xl font-semibold text-gray-900">
                    {(metrics.performance.diskUsage / (1024 ** 3)).toFixed(1)} GB
                  </div>
                  <div class="ml-2 text-sm text-gray-500">
                    of {(metrics.performance.diskTotal / (1024 ** 3)).toFixed(1)} GB
                  </div>
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>

      <!-- UPLOAD SPEED -->
      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <ArrowUp class="h-6 w-6 text-green-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">
                  Upload Speed
                </dt>
                <dd class="text-2xl font-semibold text-gray-900">
                  {formatSpeed(metrics.performance.avgUploadSpeed)}
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>

      <!-- DOWNLOAD SPEED -->
      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <ArrowDown class="h-6 w-6 text-blue-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">
                  Download Speed
                </dt>
                <dd class="text-2xl font-semibold text-gray-900">
                  {formatSpeed(metrics.performance.avgDownloadSpeed)}
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>

      <!-- MEMORY STATS -->
      <div class="bg-white overflow-hidden shadow rounded-lg">
        <div class="p-5">
          <div class="flex items-center">
            <div class="flex-shrink-0">
              <Activity class="h-6 w-6 text-purple-400" />
            </div>
            <div class="ml-5 w-0 flex-1">
              <dl>
                <dt class="text-sm font-medium text-gray-500 truncate">
                  Memory Usage
                </dt>
                <dd class="flex items-baseline">
                  <div class="text-2xl font-semibold text-gray-900">
                    {(metrics.performance.memoryUsage / (1024 * 1024)).toFixed(1)} MB
                  </div>
                  <div class="ml-2 text-sm text-gray-500">
                    of {(metrics.performance.memoryTotal / (1024 * 1024)).toFixed(1)} MB
                  </div>
                </dd>
              </dl>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- CHARTS GRID -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- TRANSFER SPEEDS LINE CHART -->
      <div class="bg-white p-6 rounded-lg shadow">
        <LineChart data={lineChartData} options={lineChartOptions} />
      </div>

      <!-- TRANSFER STATS BAR CHART -->
      <div class="bg-white p-6 rounded-lg shadow">
        <BarChartStacked
          data={transferStatsData}
          options={transferStatsOptions}
        />
      </div>
    </div>

    <!-- STORAGE DONUT -->
    <div class="bg-white p-6 rounded-lg shadow">
      <DonutChart data={storageData} options={storageDonutOptions} />
    </div>

    <!-- DETAILED STATS -->
    <div class="bg-white shadow rounded-lg">
      <div class="px-4 py-5 sm:p-6">
        <h4 class="text-base font-medium text-gray-900 mb-4">
          Transfer Details
        </h4>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <!-- UPLOAD -->
          <div>
            <h5 class="text-sm font-medium text-gray-500 mb-2">
              Upload Statistics
            </h5>
            <dl class="space-y-2">
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Total Uploads</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {metrics.blobUploads.total}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Failed Uploads</dt>
                <dd class="text-sm font-medium text-red-600">
                  {metrics.blobUploads.failed}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Average Duration</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {(metrics.blobUploads.avgDuration / 1000).toFixed(2)}s
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Total Data Uploaded</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {formatBytes(metrics.blobUploads.bytesProcessed)}
                </dd>
              </div>
            </dl>
          </div>

          <!-- DOWNLOAD -->
          <div>
            <h5 class="text-sm font-medium text-gray-500 mb-2">
              Download Statistics
            </h5>
            <dl class="space-y-2">
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Total Downloads</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {metrics.blobDownloads.total}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Failed Downloads</dt>
                <dd class="text-sm font-medium text-red-600">
                  {metrics.blobDownloads.failed}
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Average Duration</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {(metrics.blobDownloads.avgDuration / 1000).toFixed(2)}s
                </dd>
              </div>
              <div class="flex justify-between">
                <dt class="text-sm text-gray-600">Total Data Downloaded</dt>
                <dd class="text-sm font-medium text-gray-900">
                  {formatBytes(metrics.blobDownloads.bytesProcessed)}
                </dd>
              </div>
            </dl>
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>
