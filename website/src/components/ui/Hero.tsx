import React from 'react';
import { cn } from './lib/utils';

interface HeroProps extends React.HTMLAttributes<HTMLDivElement> {
  title: string;
  description?: string;
  children?: React.ReactNode;
  eyebrow?: string;
}

function Hero({ title, description, children, eyebrow, className, ...props }: HeroProps) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center px-4 py-24 text-center',
        className,
      )}
      {...props}
    >
      {eyebrow && (
        <p className="mb-4 text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          {eyebrow}
        </p>
      )}
      <h1
        className="mx-auto max-w-3xl text-4xl font-semibold tracking-tight text-foreground sm:text-5xl md:text-6xl"
        style={{ letterSpacing: '-0.04em' }}
      >
        {title}
      </h1>
      {description && (
        <p className="mx-auto mt-6 max-w-2xl text-lg text-muted-foreground leading-relaxed">
          {description}
        </p>
      )}
      {children && (
        <div className="mt-8 flex flex-col items-center gap-4 sm:flex-row sm:justify-center">
          {children}
        </div>
      )}
    </div>
  );
}

export { Hero };
