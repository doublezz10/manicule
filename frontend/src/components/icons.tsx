import React from "react";

/**
 * Brand marks drawn for manicule — no icon library, so the app carries
 * its own hand. All icons inherit currentColor and size via props.
 */

export function ManiculeMark(props: { size?: number; className?: string }) {
  const s = props.size ?? 24;
  // Silhouette union: cuff, thumb, fist, index finger — reads at 16px and 96px.
  return (
    <svg
      className={props.className}
      width={s}
      height={(s * 48) / 64}
      viewBox="0 0 64 48"
      fill="currentColor"
      aria-hidden="true"
      style={{ flexShrink: 0 }}
    >
      <rect x="2" y="13" width="15" height="28" rx="4.5" />
      <circle cx="21" cy="11.5" r="6.5" />
      <rect x="14" y="7" width="29" height="35" rx="9" />
      <rect x="33" y="14.5" width="29" height="12" rx="6" />
    </svg>
  );
}

function base(size?: number) {
  return {
    width: size ?? 18,
    height: size ?? 18,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.7,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true as const,
    style: { flexShrink: 0 as const },
  };
}

export function SearchIcon(props: { size?: number }) {
  return (
    <svg {...base(props.size)}>
      <circle cx="10.5" cy="10.5" r="6.75" />
      <path d="M15.6 15.6 21 21" />
    </svg>
  );
}

export function QueueIcon(props: { size?: number }) {
  return (
    <svg {...base(props.size)}>
      <path d="M12 3v10.5" />
      <path d="m7.5 9.5 4.5 4.5 4.5-4.5" />
      <path d="M4 16.5v2A2.5 2.5 0 0 0 6.5 21h11a2.5 2.5 0 0 0 2.5-2.5v-2" />
    </svg>
  );
}

export function LibraryIcon(props: { size?: number }) {
  return (
    <svg {...base(props.size)}>
      <rect x="3.5" y="4" width="4.4" height="16" rx="1" />
      <rect x="9.9" y="4" width="4.4" height="16" rx="1" />
      <path d="m17.1 5.4 4.2 14" />
    </svg>
  );
}

export function DevicesIcon(props: { size?: number }) {
  return (
    <svg {...base(props.size)}>
      <rect x="5" y="2.75" width="14" height="18.5" rx="2.5" />
      <path d="M8.5 7.5h7M8.5 10.5h7M8.5 13.5h4" />
      <circle cx="12" cy="17.75" r="0.4" fill="currentColor" />
    </svg>
  );
}

export function SettingsIcon(props: { size?: number }) {
  return (
    <svg {...base(props.size)}>
      <path d="M4 7h3.6M12.4 7H20" />
      <circle cx="10" cy="7" r="2.1" />
      <path d="M4 12h9.6M18.4 12H20" />
      <circle cx="16" cy="12" r="2.1" />
      <path d="M4 17h1.6M10.4 17H20" />
      <circle cx="8" cy="17" r="2.1" />
    </svg>
  );
}
