/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      fontFamily: {
        sans: ["Geist", "Satoshi", "Cabinet Grotesk", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["Geist Mono", "JetBrains Mono", "ui-monospace", "SFMono-Regular", "monospace"],
      },
      colors: {
        ink: "#202326",
        mist: "#f7f8f6",
        line: "#dfe4df",
        accent: "#3f8f69",
      },
      boxShadow: {
        diffusion: "0 20px 40px -24px rgba(22, 35, 27, 0.24)",
      },
      keyframes: {
        "soft-pulse": {
          "0%, 100%": { opacity: "0.62", transform: "scale(1)" },
          "50%": { opacity: "1", transform: "scale(1.06)" },
        },
        shimmer: {
          "0%": { transform: "translateX(-100%)" },
          "100%": { transform: "translateX(100%)" },
        },
        rise: {
          "0%": { opacity: "0", transform: "translateY(10px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
      },
      animation: {
        "soft-pulse": "soft-pulse 2.8s cubic-bezier(0.16, 1, 0.3, 1) infinite",
        shimmer: "shimmer 1.6s cubic-bezier(0.16, 1, 0.3, 1) infinite",
        rise: "rise 420ms cubic-bezier(0.16, 1, 0.3, 1) both",
      },
    },
  },
  plugins: [],
};
