<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    code?: string;
    size?: number;
  }>(),
  {
    code: "",
    size: 16,
  },
);

const cc = computed(() => (props.code || "").trim().toUpperCase());
const w = computed(() => Math.round(props.size));
const h = computed(() => Math.round((props.size * 3) / 4));
</script>

<template>
  <svg
    class="co-flag-icon"
    :width="w"
    :height="h"
    viewBox="0 0 24 18"
    preserveAspectRatio="none"
    aria-hidden="true"
    focusable="false"
  >
    <!-- United States -->
    <g v-if="cc === 'US'">
      <rect width="24" height="18" fill="#ffffff" />
      <path
        fill="#b22234"
        d="M0 0h24v1.385H0zm0 2.769h24v1.385H0zm0 2.769h24v1.385H0zm0 2.769h24v1.385H0zm0 2.769h24v1.385H0zm0 2.769h24v1.385H0zm0 2.77h24V18H0z"
      />
      <rect width="10.4" height="9.692" fill="#3c3b6e" />
      <g fill="#ffffff">
        <circle cx="1.8" cy="1.7" r="0.55" />
        <circle cx="3.9" cy="1.7" r="0.55" />
        <circle cx="6.0" cy="1.7" r="0.55" />
        <circle cx="8.1" cy="1.7" r="0.55" />
        <circle cx="2.85" cy="3.3" r="0.55" />
        <circle cx="4.95" cy="3.3" r="0.55" />
        <circle cx="7.05" cy="3.3" r="0.55" />
        <circle cx="1.8" cy="4.85" r="0.55" />
        <circle cx="3.9" cy="4.85" r="0.55" />
        <circle cx="6.0" cy="4.85" r="0.55" />
        <circle cx="8.1" cy="4.85" r="0.55" />
        <circle cx="2.85" cy="6.4" r="0.55" />
        <circle cx="4.95" cy="6.4" r="0.55" />
        <circle cx="7.05" cy="6.4" r="0.55" />
        <circle cx="1.8" cy="8.0" r="0.55" />
        <circle cx="3.9" cy="8.0" r="0.55" />
        <circle cx="6.0" cy="8.0" r="0.55" />
        <circle cx="8.1" cy="8.0" r="0.55" />
      </g>
    </g>

    <!-- Taiwan (asia-east1) -->
    <g v-else-if="cc === 'TW'">
      <rect width="24" height="18" fill="#fe0000" />
      <rect width="12" height="9" fill="#000095" />
      <polygon
        fill="#ffffff"
        points="6,1.2 6.65,3.1 8.35,2.15 7.4,3.85 9.3,4.5 7.4,5.15 8.35,6.85 6.65,5.9 6,7.8 5.35,5.9 3.65,6.85 4.6,5.15 2.7,4.5 4.6,3.85 3.65,2.15 5.35,3.1"
      />
      <circle cx="6" cy="4.5" r="1.95" fill="#000095" />
      <circle cx="6" cy="4.5" r="1.5" fill="#ffffff" />
    </g>

    <!-- Hong Kong (asia-east2) -->
    <g v-else-if="cc === 'HK'">
      <rect width="24" height="18" fill="#de2910" />
      <g fill="#ffffff" transform="translate(12 9)">
        <ellipse cx="0" cy="-3.1" rx="1.25" ry="2.5" transform="rotate(15)" />
        <ellipse cx="0" cy="-3.1" rx="1.25" ry="2.5" transform="rotate(87)" />
        <ellipse cx="0" cy="-3.1" rx="1.25" ry="2.5" transform="rotate(159)" />
        <ellipse cx="0" cy="-3.1" rx="1.25" ry="2.5" transform="rotate(231)" />
        <ellipse cx="0" cy="-3.1" rx="1.25" ry="2.5" transform="rotate(303)" />
      </g>
    </g>

    <!-- China -->
    <g v-else-if="cc === 'CN'">
      <rect width="24" height="18" fill="#ee1c25" />
      <polygon
        fill="#ffff00"
        points="4.8,2.2 5.48,4.3 7.7,4.3 5.9,5.6 6.58,7.7 4.8,6.4 3.02,7.7 3.7,5.6 1.9,4.3 4.12,4.3"
      />
      <g fill="#ffff00">
        <circle cx="9.2" cy="2.1" r="0.65" />
        <circle cx="10.7" cy="3.7" r="0.65" />
        <circle cx="10.7" cy="5.9" r="0.65" />
        <circle cx="9.2" cy="7.5" r="0.65" />
      </g>
    </g>

    <!-- Singapore (asia-southeast1) -->
    <g v-else-if="cc === 'SG'">
      <rect width="24" height="9" fill="#ef3340" />
      <rect y="9" width="24" height="9" fill="#ffffff" />
      <circle cx="5.4" cy="4.5" r="3.1" fill="#ffffff" />
      <circle cx="6.5" cy="4.5" r="2.8" fill="#ef3340" />
      <g fill="#ffffff">
        <circle cx="7.4" cy="2.9" r="0.45" />
        <circle cx="8.8" cy="3.9" r="0.45" />
        <circle cx="8.3" cy="5.5" r="0.45" />
        <circle cx="6.5" cy="5.5" r="0.45" />
        <circle cx="6.0" cy="3.9" r="0.45" />
      </g>
    </g>

    <!-- Japan (asia-northeast1/2) -->
    <g v-else-if="cc === 'JP'">
      <rect width="24" height="18" fill="#ffffff" />
      <circle cx="12" cy="9" r="4.8" fill="#bc002d" />
    </g>

    <!-- South Korea (asia-northeast3) -->
    <g v-else-if="cc === 'KR'">
      <rect width="24" height="18" fill="#ffffff" />
      <circle cx="12" cy="9" r="4.2" fill="#cd2e3a" />
      <path
        d="M7.8 9a4.2 4.2 0 0 0 8.4 0 2.1 2.1 0 0 0-4.2 0 2.1 2.1 0 0 1-4.2 0z"
        fill="#0047a0"
      />
      <g stroke="#111111" stroke-width="0.9">
        <path d="M4.5 5.2l2.2-1.8M5.2 6.1l2.2-1.8" />
        <path d="M19.5 5.2l-2.2-1.8M18.8 6.1l-2.2-1.8" />
        <path d="M4.5 12.8l2.2 1.8M5.2 11.9l2.2 1.8" />
        <path d="M19.5 12.8l-2.2 1.8M18.8 11.9l-2.2 1.8" />
      </g>
    </g>

    <!-- Netherlands (europe-west4) -->
    <g v-else-if="cc === 'NL'">
      <rect width="24" height="6" fill="#ae1c28" />
      <rect y="6" width="24" height="6" fill="#ffffff" />
      <rect y="12" width="24" height="6" fill="#21468b" />
    </g>

    <!-- Belgium (europe-west1) -->
    <g v-else-if="cc === 'BE'">
      <rect width="8" height="18" fill="#2d2926" />
      <rect x="8" width="8" height="18" fill="#ffcd00" />
      <rect x="16" width="8" height="18" fill="#c8102e" />
    </g>

    <!-- Germany (europe-west3/10) -->
    <g v-else-if="cc === 'DE'">
      <rect width="24" height="6" fill="#000000" />
      <rect y="6" width="24" height="6" fill="#dd0000" />
      <rect y="12" width="24" height="6" fill="#ffce00" />
    </g>

    <!-- France (europe-west9) -->
    <g v-else-if="cc === 'FR'">
      <rect width="8" height="18" fill="#002395" />
      <rect x="8" width="8" height="18" fill="#ffffff" />
      <rect x="16" width="8" height="18" fill="#ed2939" />
    </g>

    <!-- Italy (europe-west8/12) -->
    <g v-else-if="cc === 'IT'">
      <rect width="8" height="18" fill="#009246" />
      <rect x="8" width="8" height="18" fill="#ffffff" />
      <rect x="16" width="8" height="18" fill="#ce2b37" />
    </g>

    <!-- Ireland -->
    <g v-else-if="cc === 'IE'">
      <rect width="8" height="18" fill="#169b62" />
      <rect x="8" width="8" height="18" fill="#ffffff" />
      <rect x="16" width="8" height="18" fill="#ff883e" />
    </g>

    <!-- United Kingdom (europe-west2) -->
    <g v-else-if="cc === 'GB' || cc === 'UK'">
      <rect width="24" height="18" fill="#012169" />
      <path d="M0 0l24 18M24 0L0 18" stroke="#ffffff" stroke-width="3.6" />
      <path d="M0 0l24 18M24 0L0 18" stroke="#c8102e" stroke-width="1.8" />
      <path d="M12 0v18M0 9h24" stroke="#ffffff" stroke-width="5.6" />
      <path d="M12 0v18M0 9h24" stroke="#c8102e" stroke-width="3.2" />
    </g>

    <!-- Switzerland (europe-west6) -->
    <g v-else-if="cc === 'CH'">
      <rect width="24" height="18" fill="#da291c" />
      <rect x="10.2" y="3.8" width="3.6" height="10.4" fill="#ffffff" />
      <rect x="6.8" y="7.2" width="10.4" height="3.6" fill="#ffffff" />
    </g>

    <!-- Spain (europe-southwest1) -->
    <g v-else-if="cc === 'ES'">
      <rect width="24" height="4.5" fill="#aa151b" />
      <rect y="4.5" width="24" height="9" fill="#f1bf00" />
      <rect y="13.5" width="24" height="4.5" fill="#aa151b" />
      <rect x="5.5" y="6.8" width="2.8" height="4.2" rx="1" fill="#aa151b" />
    </g>

    <!-- Poland (europe-central2) -->
    <g v-else-if="cc === 'PL'">
      <rect width="24" height="9" fill="#ffffff" />
      <rect y="9" width="24" height="9" fill="#dc143c" />
    </g>

    <!-- Indonesia (asia-southeast2) -->
    <g v-else-if="cc === 'ID'">
      <rect width="24" height="9" fill="#ce1126" />
      <rect y="9" width="24" height="9" fill="#ffffff" />
    </g>

    <!-- Finland (europe-north1) -->
    <g v-else-if="cc === 'FI'">
      <rect width="24" height="18" fill="#ffffff" />
      <rect x="6.5" width="4" height="18" fill="#003580" />
      <rect y="7" width="24" height="4" fill="#003580" />
    </g>

    <!-- Sweden (europe-north2) -->
    <g v-else-if="cc === 'SE'">
      <rect width="24" height="18" fill="#006aa7" />
      <rect x="6.5" width="3.6" height="18" fill="#fecc02" />
      <rect y="7.2" width="24" height="3.6" fill="#fecc02" />
    </g>

    <!-- Norway -->
    <g v-else-if="cc === 'NO'">
      <rect width="24" height="18" fill="#ba0c2f" />
      <rect x="6" width="5" height="18" fill="#ffffff" />
      <rect y="6.5" width="24" height="5" fill="#ffffff" />
      <rect x="7.2" width="2.6" height="18" fill="#00205b" />
      <rect y="7.7" width="24" height="2.6" fill="#00205b" />
    </g>

    <!-- Denmark -->
    <g v-else-if="cc === 'DK'">
      <rect width="24" height="18" fill="#c8102e" />
      <rect x="6.8" width="3.2" height="18" fill="#ffffff" />
      <rect y="7.4" width="24" height="3.2" fill="#ffffff" />
    </g>

    <!-- Canada (northamerica-northeast1/2) -->
    <g v-else-if="cc === 'CA'">
      <rect width="6" height="18" fill="#ff0000" />
      <rect x="6" width="12" height="18" fill="#ffffff" />
      <rect x="18" width="6" height="18" fill="#ff0000" />
      <path
        fill="#ff0000"
        d="M12 3.8l1.2 2.5 2.4-.5-1.2 2.4 1.6 1.1-2.8.5.2 2.4h-2.8l.2-2.4-2.8-.5 1.6-1.1-1.2-2.4 2.4.5z"
      />
      <rect x="11.5" y="11.5" width="1" height="2.8" fill="#ff0000" />
    </g>

    <!-- Mexico (northamerica-south1) -->
    <g v-else-if="cc === 'MX'">
      <rect width="8" height="18" fill="#006847" />
      <rect x="8" width="8" height="18" fill="#ffffff" />
      <rect x="16" width="8" height="18" fill="#ce1126" />
      <circle cx="12" cy="9" r="1.8" fill="#8c5a2b" />
    </g>

    <!-- Brazil (southamerica-east1) -->
    <g v-else-if="cc === 'BR'">
      <rect width="24" height="18" fill="#009c3b" />
      <polygon points="12,2.2 21.6,9 12,15.8 2.4,9" fill="#ffdf00" />
      <circle cx="12" cy="9" r="3.7" fill="#002776" />
      <path
        d="M8.5 8.2q3.6-1.2 7 1.3"
        stroke="#ffffff"
        stroke-width="1"
        fill="none"
      />
    </g>

    <!-- Chile (southamerica-west1) -->
    <g v-else-if="cc === 'CL'">
      <rect width="24" height="9" fill="#ffffff" />
      <rect width="9" height="9" fill="#0039a6" />
      <rect y="9" width="24" height="9" fill="#d52b1e" />
      <polygon
        fill="#ffffff"
        points="4.5,2 5.1,3.8 7,3.8 5.5,4.9 6.1,6.7 4.5,5.6 2.9,6.7 3.5,4.9 2,3.8 3.9,3.8"
      />
    </g>

    <!-- India (asia-south1/2) -->
    <g v-else-if="cc === 'IN'">
      <rect width="24" height="6" fill="#ff9933" />
      <rect y="6" width="24" height="6" fill="#ffffff" />
      <rect y="12" width="24" height="6" fill="#138808" />
      <circle
        cx="12"
        cy="9"
        r="2.1"
        fill="none"
        stroke="#000080"
        stroke-width="0.9"
      />
      <circle cx="12" cy="9" r="0.6" fill="#000080" />
    </g>

    <!-- Australia (australia-southeast1/2) -->
    <g v-else-if="cc === 'AU'">
      <rect width="24" height="18" fill="#00008b" />
      <path d="M0 0l12 9M12 0L0 9" stroke="#ffffff" stroke-width="2.2" />
      <path d="M0 0l12 9M12 0L0 9" stroke="#c8102e" stroke-width="1.1" />
      <path d="M6 0v9M0 4.5h12" stroke="#ffffff" stroke-width="3" />
      <path d="M6 0v9M0 4.5h12" stroke="#c8102e" stroke-width="1.7" />
      <g fill="#ffffff">
        <circle cx="6" cy="13.5" r="1.5" />
        <circle cx="18" cy="4.2" r="0.9" />
        <circle cx="20.6" cy="7.5" r="0.8" />
        <circle cx="15.6" cy="8.4" r="0.8" />
        <circle cx="18" cy="13.5" r="0.95" />
      </g>
    </g>

    <!-- Israel (me-west1) -->
    <g v-else-if="cc === 'IL'">
      <rect width="24" height="18" fill="#ffffff" />
      <rect y="1.8" width="24" height="2.4" fill="#0038b8" />
      <rect y="13.8" width="24" height="2.4" fill="#0038b8" />
      <polygon
        points="12,5.4 15.2,10.8 8.8,10.8"
        fill="none"
        stroke="#0038b8"
        stroke-width="1"
      />
      <polygon
        points="12,12.6 15.2,7.2 8.8,7.2"
        fill="none"
        stroke="#0038b8"
        stroke-width="1"
      />
    </g>

    <!-- Qatar (me-central1) -->
    <g v-else-if="cc === 'QA'">
      <rect width="24" height="18" fill="#8a1538" />
      <polygon
        fill="#ffffff"
        points="0,0 7,0 8.8,1 7,2 8.8,3 7,4 8.8,5 7,6 8.8,7 7,8 8.8,9 7,10 8.8,11 7,12 8.8,13 7,14 8.8,15 7,16 8.8,17 7,18 0,18"
      />
    </g>

    <!-- Saudi Arabia (me-central2) -->
    <g v-else-if="cc === 'SA'">
      <rect width="24" height="18" fill="#006c35" />
      <rect x="5" y="6" width="14" height="2.4" rx="1" fill="#ffffff" />
      <rect x="6.5" y="10.8" width="11" height="1.2" rx="0.6" fill="#ffffff" />
    </g>

    <!-- United Arab Emirates -->
    <g v-else-if="cc === 'AE'">
      <rect width="24" height="6" fill="#00732f" />
      <rect y="6" width="24" height="6" fill="#ffffff" />
      <rect y="12" width="24" height="6" fill="#000000" />
      <rect width="6.5" height="18" fill="#ff0000" />
    </g>

    <!-- South Africa (africa-south1) -->
    <g v-else-if="cc === 'ZA'">
      <rect width="24" height="9" fill="#e03c31" />
      <rect y="9" width="24" height="9" fill="#001489" />
      <path
        d="M0 0l10.5 9L0 18"
        fill="none"
        stroke="#ffffff"
        stroke-width="5.2"
      />
      <path d="M10.5 9H24" stroke="#ffffff" stroke-width="5.2" />
      <path
        d="M0 0l10.5 9L0 18"
        fill="none"
        stroke="#007749"
        stroke-width="3.2"
      />
      <path d="M10.5 9H24" stroke="#007749" stroke-width="3.2" />
      <polygon points="0,2.2 7.8,9 0,15.8" fill="#ffb81c" />
      <polygon points="0,3.8 6,9 0,14.2" fill="#000000" />
    </g>

    <!-- Austria -->
    <g v-else-if="cc === 'AT'">
      <rect width="24" height="6" fill="#ed2939" />
      <rect y="6" width="24" height="6" fill="#ffffff" />
      <rect y="12" width="24" height="6" fill="#ed2939" />
    </g>

    <!-- Russia -->
    <g v-else-if="cc === 'RU'">
      <rect width="24" height="6" fill="#ffffff" />
      <rect y="6" width="24" height="6" fill="#0039a6" />
      <rect y="12" width="24" height="6" fill="#d52b1e" />
    </g>

    <!-- Thailand -->
    <g v-else-if="cc === 'TH'">
      <rect width="24" height="3" fill="#a51931" />
      <rect y="3" width="24" height="3" fill="#f4f5f8" />
      <rect y="6" width="24" height="6" fill="#2d2a4a" />
      <rect y="12" width="24" height="3" fill="#f4f5f8" />
      <rect y="15" width="24" height="3" fill="#a51931" />
    </g>

    <!-- Vietnam -->
    <g v-else-if="cc === 'VN'">
      <rect width="24" height="18" fill="#da251d" />
      <polygon
        fill="#ffff00"
        points="12,3.6 13.3,7.5 17.4,7.5 14.1,9.9 15.3,13.8 12,11.4 8.7,13.8 9.9,9.9 6.6,7.5 10.7,7.5"
      />
    </g>

    <!-- Malaysia (asia-southeast4) -->
    <g v-else-if="cc === 'MY'">
      <rect width="24" height="18" fill="#ffffff" />
      <path
        fill="#cc0001"
        d="M0 0h24v1.286H0zm0 2.571h24v1.286H0zm0 2.572h24v1.286H0zm0 2.571h24v1.286H0zm0 2.572h24v1.285H0zm0 2.571h24v1.286H0zm0 2.572h24V16.7H0z"
      />
      <rect width="12" height="10.286" fill="#010066" />
      <circle cx="5.2" cy="5.1" r="2.9" fill="#ffcc00" />
      <circle cx="6.1" cy="5.1" r="2.5" fill="#010066" />
      <circle cx="8.2" cy="5.1" r="1.3" fill="#ffcc00" />
    </g>

    <!-- New Zealand -->
    <g v-else-if="cc === 'NZ'">
      <rect width="24" height="18" fill="#00247d" />
      <path d="M0 0l12 9M12 0L0 9" stroke="#ffffff" stroke-width="2.2" />
      <path d="M0 0l12 9M12 0L0 9" stroke="#cc142b" stroke-width="1.1" />
      <path d="M6 0v9M0 4.5h12" stroke="#ffffff" stroke-width="3" />
      <path d="M6 0v9M0 4.5h12" stroke="#cc142b" stroke-width="1.7" />
      <g fill="#cc142b" stroke="#ffffff" stroke-width="0.35">
        <circle cx="18" cy="4" r="1" />
        <circle cx="20.5" cy="7.5" r="0.9" />
        <circle cx="15.8" cy="8.2" r="0.9" />
        <circle cx="18" cy="13.5" r="1.05" />
      </g>
    </g>

    <!-- Portugal -->
    <g v-else-if="cc === 'PT'">
      <rect width="9.6" height="18" fill="#006600" />
      <rect x="9.6" width="14.4" height="18" fill="#ff0000" />
      <circle cx="9.6" cy="9" r="3.1" fill="#ffcc00" />
      <rect x="8.3" y="7.5" width="2.6" height="3" rx="0.6" fill="#ffffff" />
    </g>

    <!-- Turkey -->
    <g v-else-if="cc === 'TR'">
      <rect width="24" height="18" fill="#e30a17" />
      <circle cx="9.6" cy="9" r="4.3" fill="#ffffff" />
      <circle cx="10.8" cy="9" r="3.4" fill="#e30a17" />
      <polygon
        fill="#ffffff"
        points="14.8,6.8 15.4,8.4 17.1,8.4 15.7,9.4 16.2,11 14.8,10 13.4,11 13.9,9.4 12.5,8.4 14.2,8.4"
      />
    </g>

    <!-- Ukraine -->
    <g v-else-if="cc === 'UA'">
      <rect width="24" height="9" fill="#0057b7" />
      <rect y="9" width="24" height="9" fill="#ffd700" />
    </g>

    <!-- Fallback globe badge for any other ISO region -->
    <g v-else>
      <rect width="24" height="18" fill="#2563eb" />
      <circle
        cx="12"
        cy="9"
        r="5.5"
        fill="none"
        stroke="#ffffff"
        stroke-width="1.2"
      />
      <ellipse
        cx="12"
        cy="9"
        rx="2.7"
        ry="5.5"
        fill="none"
        stroke="#ffffff"
        stroke-width="1.1"
      />
      <path d="M6.7 9h10.6" stroke="#ffffff" stroke-width="1.1" />
    </g>
  </svg>
</template>
