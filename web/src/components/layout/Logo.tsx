import { cn } from '../../lib/utils';

interface LogoProps {
  size?: number;
  className?: string;
  showText?: boolean;
}

/** Compass rose logo representing navigation and direction. */
export function Logo({ size = 32, className, showText = false }: LogoProps) {
  const half = size / 2;
  const outerR = half * 0.92;
  const midR = half * 0.72;
  const innerR = half * 0.52;
  const starOuter = half * 0.82;
  const starInner = half * 0.22;

  return (
    <span className={cn('inline-flex items-center gap-[var(--space-2)]', className)}>
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        role="img"
        aria-label="Azimuthal logo"
      >
        <defs>
          <filter id="logo-glow" x="-40%" y="-40%" width="180%" height="180%">
            <feGaussianBlur stdDeviation="1.5" result="blur" />
            <feComposite in="SourceGraphic" in2="blur" operator="over" />
          </filter>
        </defs>

        {/* Background circle */}
        <circle cx={half} cy={half} r={outerR} fill="#1A1D27" />

        {/* Outer ring */}
        <circle
          cx={half}
          cy={half}
          r={outerR}
          fill="none"
          stroke="#2A3A5C"
          strokeWidth={1.2}
        />

        {/* Middle ring */}
        <circle
          cx={half}
          cy={half}
          r={midR}
          fill="none"
          stroke="#35507A"
          strokeWidth={0.8}
        />

        {/* Inner ring */}
        <circle
          cx={half}
          cy={half}
          r={innerR}
          fill="none"
          stroke="#4A90D9"
          strokeWidth={0.6}
          opacity={0.6}
        />

        {/* 4-pointed compass star (N/S/E/W) */}
        <g filter="url(#logo-glow)">
          <polygon
            points={[
              `${half},${half - starOuter}`,
              `${half + starInner},${half - starInner}`,
              `${half + starOuter},${half}`,
              `${half + starInner},${half + starInner}`,
              `${half},${half + starOuter}`,
              `${half - starInner},${half + starInner}`,
              `${half - starOuter},${half}`,
              `${half - starInner},${half - starInner}`,
            ].join(' ')}
            fill="#4A90D9"
          />

          {/* Lighter north point overlay */}
          <polygon
            points={`${half},${half - starOuter} ${half + starInner},${half - starInner} ${half},${half}`}
            fill="#6BA8E8"
            opacity={0.7}
          />

          {/* Lighter east point overlay */}
          <polygon
            points={`${half + starOuter},${half} ${half + starInner},${half - starInner} ${half},${half}`}
            fill="#6BA8E8"
            opacity={0.4}
          />
        </g>

        {/* Center dot */}
        <circle cx={half} cy={half} r={half * 0.08} fill="#E8EAF0" />
      </svg>

      {showText && (
        <span
          className="font-semibold tracking-tight text-[var(--color-text)]"
          style={{ fontFamily: 'var(--font-sans)', fontSize: 'var(--text-lg)' }}
        >
          Azimuthal
        </span>
      )}
    </span>
  );
}
