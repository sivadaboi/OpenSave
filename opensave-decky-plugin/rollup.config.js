import resolve from '@rollup/plugin-node-resolve';
import commonjs from '@rollup/plugin-commonjs';
import typescript from '@rollup/plugin-typescript';

export default {
  input: 'src/index.tsx',
  output: {
    file: 'dist/index.js',
    format: 'iife',
    name: 'plugin',
    globals: {
      react: 'React',
      'react-dom': 'ReactDOM'
    }
  },
  external: ['react', 'react-dom', 'decky-frontend-lib'],
  plugins: [
    resolve(),
    commonjs(),
    typescript()
  ]
};
